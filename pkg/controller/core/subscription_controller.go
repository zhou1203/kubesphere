/*
Copyright 2022 KubeSphere Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package core

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/clientcmd"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"helm.sh/helm/v3/pkg/getter"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	"kubesphere.io/utils/helm"
)

const (
	subscriptionController          = "subscription-controller"
	subscriptionFinalizer           = "subscriptions.kubesphere.io"
	systemWorkspace                 = "system-workspace"
	targetNamespaceFormat           = "extension-%s"
	defaultRole                     = "kubesphere:helm-executor"
	defaultRoleBinding              = "kubesphere:helm-executor"
	defaultClusterRoleFormat        = "kubesphere:%s:helm-executor"
	permissionDefinitionFile        = "permissions.yaml"
	defaultClusterRoleBindingFormat = defaultClusterRoleFormat
)

var _ reconcile.Reconciler = &SubscriptionReconciler{}

type SubscriptionReconciler struct {
	client.Client
	kubeconfig string
	helmGetter getter.Getter
	recorder   record.EventRecorder
	logger     logr.Logger
}

func NewSubscriptionReconciler(kubeconfigPath string) (*SubscriptionReconciler, error) {
	// TODO support more options (e.g. skipTLSVerify or basic auth etc.) for the specified repository
	helmGetter, _ := getter.NewHTTPGetter()

	var kubeconfig string
	if kubeconfigPath != "" {
		data, err := os.ReadFile(kubeconfigPath)
		if err != nil {
			return nil, err
		}
		kubeconfig = string(data)
	}

	return &SubscriptionReconciler{kubeconfig: kubeconfig, helmGetter: helmGetter}, nil
}

func (r *SubscriptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("subscription", req.String())
	logger.V(4).Info("sync subscription")

	sub := &corev1alpha1.Subscription{}
	if err := r.Client.Get(ctx, req.NamespacedName, sub); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctx = klog.NewContext(ctx, logger)

	if !controllerutil.ContainsFinalizer(sub, subscriptionFinalizer) {
		expected := sub.DeepCopy()
		controllerutil.AddFinalizer(expected, subscriptionFinalizer)
		return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(sub))
	}

	if !sub.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, sub)
	}

	if err := r.syncSubscriptionStatus(ctx, sub); err != nil {
		logger.Error(err, "failed to sync subscription status")
		return ctrl.Result{}, err
	}

	// Multicluster installation
	if sub.Spec.ClusterScheduling != nil {
		if err := r.syncClusterSchedulingStatus(ctx, sub); err != nil {
			logger.Error(err, "failed to scheduling status")
			return ctrl.Result{}, err
		}
	}

	r.logger.V(4).Info("Successfully synced")
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(subscriptionController)
	r.recorder = mgr.GetEventRecorderFor(subscriptionController)
	controller, err := ctrl.NewControllerManagedBy(mgr).
		Named(subscriptionController).
		For(&corev1alpha1.Subscription{}).Build(r)
	if err != nil {
		return err
	}

	labelSelector, _ := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{{
			Key:      corev1alpha1.SubscriptionReferenceLabel,
			Operator: metav1.LabelSelectorOpExists,
		}}})

	err = controller.Watch(
		&source.Kind{Type: &batchv1.Job{}},
		handler.EnqueueRequestsFromMapFunc(
			func(h client.Object) []reconcile.Request {
				return []reconcile.Request{{
					NamespacedName: types.NamespacedName{
						Name: h.GetLabels()[corev1alpha1.SubscriptionReferenceLabel],
					}}}
			}),
		predicate.And(labelSelector, predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldJob := e.ObjectOld.(*batchv1.Job)
				newJob := e.ObjectNew.(*batchv1.Job)
				return !reflect.DeepEqual(oldJob.Status, newJob.Status)
			},
			CreateFunc: func(e event.CreateEvent) bool {
				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
		}))

	if err != nil {
		return err
	}

	return nil
}

func (r *SubscriptionReconciler) defaultHelmOptions() []helm.Option {
	options := make([]helm.Option, 0)
	// TODO support helm image option
	return options
}

// reconcileDelete delete the helm release involved and remove finalizer from subscription.
func (r *SubscriptionReconciler) reconcileDelete(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	logger := klog.FromContext(ctx)

	// It has not been installed correctly.
	if sub.Status.ReleaseName == "" {
		if err := r.postRemove(ctx, sub); err != nil {
			logger.Error(err, "failed to post remove")
			return ctrl.Result{}, err
		}
	}

	for clusterName, clusterSchedulingStatus := range sub.Status.ClusterSchedulingStatuses {
		if err := r.uninstall(ctx, sub, clusterName); err != nil {
			updateClusterSchedulingStateAndCondition(sub, clusterName, &clusterSchedulingStatus, corev1alpha1.StateUninstalled, "")
			if err := r.updateSubscription(ctx, sub); err != nil {
				logger.Error(err, "failed to update scheduling state and conditions")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	helmExecutor, err := helm.NewExecutor(r.kubeconfig, sub.Status.TargetNamespace, sub.Status.ReleaseName, r.defaultHelmOptions()...)
	if err != nil {
		logger.Error(err, "failed to create helm executor")
		return ctrl.Result{}, err
	}

	if sub.Status.State != corev1alpha1.StateInstalled ||
		sub.Status.State == corev1alpha1.StateUninstalling {
		if err = helmExecutor.ForceDelete(ctx); err != nil {
			logger.Error(err, "failed to create force delete")
			return ctrl.Result{}, err
		}
		if err = r.postRemove(ctx, sub); err != nil {
			logger.Error(err, "failed to post remove")
			return ctrl.Result{}, err
		}
	}

	if _, err = helmExecutor.Manifest(); err != nil {
		if strings.Contains(err.Error(), "release: not found") {
			// The involved release does not exist, just move on.
			return ctrl.Result{}, r.postRemove(ctx, sub)
		}
		return ctrl.Result{}, err
	}

	if sub.Status.State == corev1alpha1.StateInstalled {
		logger.V(4).Info("delete helm release", "namespace", targetNamespaceFormat, "release", sub.Status.ReleaseName)
		jobName, err := helmExecutor.Uninstall(ctx, helm.SetHelmJobLabels(map[string]string{
			corev1alpha1.SubscriptionReferenceLabel: sub.Name,
		}))
		if err != nil {
			logger.Error(err, "failed to delete helm release")
			updateStateAndCondition(sub, corev1alpha1.StateUninstallFailed, "")
			if err := r.updateSubscription(ctx, sub); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		sub.Status.JobName = jobName
		updateStateAndCondition(sub, corev1alpha1.StateUninstalling, "")
		if err := r.updateSubscription(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	if sub.Status.State == corev1alpha1.StateUninstalling {
		job := batchv1.Job{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: sub.Status.JobName}, &job); err != nil {
			return ctrl.Result{}, err
		}

		completed, failed := jobStatus(job)

		if completed {
			klog.V(4).Infof("remove the finalizer for subscription %s", sub.Name)
			if err = r.postRemove(ctx, sub); err != nil {
				return ctrl.Result{}, err
			}
		} else if failed {
			updateStateAndCondition(sub, corev1alpha1.StateUninstallFailed, latestJobCondition(job).Message)
			if err := r.updateSubscription(ctx, sub); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return reconcile.Result{}, nil
}

func latestJobCondition(job batchv1.Job) batchv1.JobCondition {
	jobConditions := job.Status.Conditions
	sort.Slice(jobConditions, func(i, j int) bool {
		return jobConditions[i].LastTransitionTime.After(jobConditions[j].LastTransitionTime.Time)
	})
	if len(job.Status.Conditions) > 0 {
		return jobConditions[0]
	}
	return batchv1.JobCondition{}
}

func jobStatus(job batchv1.Job) (completed, failed bool) {
	completed = (job.Spec.Completions != nil && job.Status.Succeeded >= *job.Spec.Completions) || job.Status.Succeeded > 0
	failed = (job.Spec.BackoffLimit != nil && job.Status.Failed > *job.Spec.BackoffLimit) || job.Status.Failed > 0
	return
}

func (r *SubscriptionReconciler) loadChartData(ctx context.Context, ref *corev1alpha1.ExtensionRef) ([]byte, *corev1alpha1.ExtensionVersion, error) {
	extensionVersion := &corev1alpha1.ExtensionVersion{}
	if err := r.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-%s", ref.Name, ref.Version)}, extensionVersion); err != nil {
		return nil, extensionVersion, err
	}

	// load chart data from
	if extensionVersion.Spec.ChartDataRef != nil {
		configMap := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: extensionVersion.Spec.ChartDataRef.Namespace, Name: extensionVersion.Spec.ChartDataRef.Name}, configMap); err != nil {
			return nil, extensionVersion, err
		}
		data := configMap.BinaryData[extensionVersion.Spec.ChartDataRef.Key]
		if data != nil {
			return data, extensionVersion, nil
		}
		return nil, extensionVersion, fmt.Errorf("binary data not found")
	}

	// load chart data from url
	if extensionVersion.Spec.ChartURL != "" {
		buf, err := r.helmGetter.Get(extensionVersion.Spec.ChartURL,
			getter.WithTimeout(5*time.Minute),
		)
		if err != nil {
			return nil, extensionVersion, err
		}
		return buf.Bytes(), extensionVersion, nil
	}

	return nil, extensionVersion, fmt.Errorf("unable to load chart data")
}

func (r *SubscriptionReconciler) installExtension(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)

	updateStateAndCondition(sub, corev1alpha1.StatePreparing, "")
	if err := r.updateSubscription(ctx, sub); err != nil {
		logger.Error(err, "failed to update subscription")
		return err
	}

	charData, extensionVersion, err := r.loadChartData(ctx, &sub.Spec.Extension)
	if err != nil {
		logger.Error(err, "fail to load chart data")
		return err
	}

	clusterRole, role := usesPermissions(charData)

	// TODO: reconsider how to define the target namespace
	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name
	if err := initTargetNamespace(ctx, r.Client, targetNamespace, clusterRole, role); err != nil {
		logger.Error(err, "failed to init target namespace", "namespace", targetNamespace)
		return err
	}

	options := r.defaultHelmOptions()
	options = append(options, helm.SetLabels(map[string]string{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}))
	helmExecutor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		logger.Error(err, "failed to create executor")
		return err
	}

	jobLabels := map[string]string{corev1alpha1.SubscriptionReferenceLabel: sub.Name}
	helmOptions := make([]helm.HelmOption, 0)
	helmOptions = append(helmOptions, helm.SetHelmJobLabels(jobLabels))

	if extensionVersion.Spec.InstallationMode == corev1alpha1.InstallationMulticluster {
		helmOptions = append(helmOptions, helm.SetOverrides([]string{"tags.extension=true", "extension.enabled=true"}))
	}

	var jobName string
	if _, err = helmExecutor.Manifest(); err != nil {
		// release not exists or there is something wrong with the Manifest API
		if !strings.Contains(err.Error(), "release: not found") {
			return err
		}
		logger.V(4).Info("install helm release", "namespace", targetNamespace, "release", releaseName)

		jobName, err = helmExecutor.Install(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config), helmOptions...)
		if err != nil {
			logger.Error(err, "failed to install helm release")
			return err
		}
	} else {
		// release exists, we need to upgrade it
		logger.V(4).Info("upgrade helm release", "namespace", targetNamespace, "release", releaseName)
		jobName, err = helmExecutor.Upgrade(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config), helmOptions...)
		if err != nil {
			logger.Error(err, "failed to upgrade helm release")
			return err
		}
	}

	sub.Status.ReleaseName = releaseName
	sub.Status.TargetNamespace = targetNamespace
	sub.Status.JobName = jobName
	updateStateAndCondition(sub, corev1alpha1.StateInstalling, "")
	if err := r.updateSubscription(ctx, sub); err != nil {
		return err
	}
	return nil
}

func updateStateAndCondition(sub *corev1alpha1.Subscription, state string, message string) {
	sub.Status.State = state

	newCondition := metav1.Condition{
		Type:               corev1alpha1.ConditionTypeState,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             state,
		Message:            message,
	}

	if sub.Status.Conditions == nil {
		sub.Status.Conditions = []metav1.Condition{newCondition}
		return
	}

	// We need to limit the number of Condition with type State, and if the limit is exceeded, delete the oldest one.
	stateConditions := make([]metav1.Condition, 0, 1)
	otherConditions := make([]metav1.Condition, 0, 1)
	for _, condition := range sub.Status.Conditions {
		if condition.Type == corev1alpha1.ConditionTypeState {
			stateConditions = append(stateConditions, condition)
		} else {
			otherConditions = append(otherConditions, condition)
		}
	}

	// Not exceeding the limit, we can just append it to original Conditions.
	if len(stateConditions) < corev1alpha1.MaxStateConditionNum {
		sub.Status.Conditions = append(sub.Status.Conditions, newCondition)
		return
	}

	sort.Slice(stateConditions, func(i, j int) bool {
		return stateConditions[i].LastTransitionTime.After(stateConditions[j].LastTransitionTime.Time)
	})
	stateConditions = stateConditions[:corev1alpha1.MaxStateConditionNum-1]
	stateConditions = append(stateConditions, newCondition)
	sub.Status.Conditions = append(otherConditions, stateConditions...)
}

func updateClusterSchedulingStateAndCondition(sub *corev1alpha1.Subscription, clusterName string, clusterSchedulingStatus *corev1alpha1.InstallationStatus, state, message string) {
	clusterSchedulingStatus.State = state

	newCondition := metav1.Condition{
		Type:               corev1alpha1.ConditionTypeState,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             state,
		Message:            message,
	}

	if sub.Status.ClusterSchedulingStatuses == nil {
		sub.Status.ClusterSchedulingStatuses = make(map[string]corev1alpha1.InstallationStatus)
	}

	if clusterSchedulingStatus.Conditions == nil {
		clusterSchedulingStatus.Conditions = []metav1.Condition{newCondition}
		sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
		return
	}

	// We need to limit the number of Condition with type State, and if the limit is exceeded, delete the oldest one.
	stateConditions := make([]metav1.Condition, 0, 1)
	otherConditions := make([]metav1.Condition, 0, 1)
	for _, condition := range clusterSchedulingStatus.Conditions {
		if condition.Type == corev1alpha1.ConditionTypeState {
			stateConditions = append(stateConditions, condition)
		} else {
			otherConditions = append(otherConditions, condition)
		}
	}

	// Not exceeding the limit, we can just append it to original Conditions.
	if len(stateConditions) < corev1alpha1.MaxStateConditionNum {
		clusterSchedulingStatus.Conditions = append(clusterSchedulingStatus.Conditions, newCondition)
		sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
		return
	}

	sort.Slice(stateConditions, func(i, j int) bool {
		return stateConditions[i].LastTransitionTime.After(stateConditions[j].LastTransitionTime.Time)
	})
	stateConditions = stateConditions[:corev1alpha1.MaxStateConditionNum-1]
	stateConditions = append(stateConditions, newCondition)
	clusterSchedulingStatus.Conditions = append(otherConditions, stateConditions...)

	sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
}

func (r *SubscriptionReconciler) syncExecutorJobStatus(ctx context.Context, sub *corev1alpha1.Subscription) error {
	if sub.Status.JobName == "" {
		// This is unlikely to happen in normal processes, and this is just to avoid subsequent exceptions
		return nil
	}

	logger := klog.FromContext(ctx)

	job := batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: sub.Status.JobName}, &job); err != nil {
		logger.Error(err, "failed to get job", "namespace", sub.Status.TargetNamespace, "job", sub.Status.JobName)
		return err
	}
	completed, failed := jobStatus(job)
	if completed {
		updateStateAndCondition(sub, corev1alpha1.StateInstalled, "")
		if err := r.updateSubscription(ctx, sub); err != nil {
			return err
		}
	} else if failed {
		updateStateAndCondition(sub, corev1alpha1.StateInstallFailed, latestJobCondition(job).Message)
		if err := r.updateSubscription(ctx, sub); err != nil {
			return err
		}
	}

	return nil
}

func (r *SubscriptionReconciler) syncSchedulingJobStatus(ctx context.Context, sub *corev1alpha1.Subscription, cluster clusterv1alpha1.Cluster, clusterSchedulingStatus *corev1alpha1.InstallationStatus) error {
	if clusterSchedulingStatus.JobName == "" {
		// This is unlikely to happen in normal processes, and this is just to avoid subsequent exceptions
		return nil
	}
	logger := klog.FromContext(ctx)
	job := batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: clusterSchedulingStatus.JobName}, &job); err != nil {
		return err
	}
	completed, failed := jobStatus(job)
	if completed {
		updateClusterSchedulingStateAndCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstalled, "")
		if err := r.updateSubscription(ctx, sub); err != nil {
			logger.Error(err, "failed to update subscription")
			return err
		}
	} else if failed {
		updateClusterSchedulingStateAndCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstallFailed, latestJobCondition(job).Message)
		if err := r.updateSubscription(ctx, sub); err != nil {
			logger.Error(err, "failed to update subscription")
			return err
		}
	}
	return nil
}

func (r *SubscriptionReconciler) postRemove(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)
	deletePolicy := metav1.DeletePropagationBackground
	if err := r.DeleteAllOf(ctx, &batchv1.Job{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{
			Namespace:     fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name),
			LabelSelector: labels.SelectorFromSet(labels.Set{corev1alpha1.SubscriptionReferenceLabel: sub.Name}),
		},
		DeleteOptions: client.DeleteOptions{PropagationPolicy: &deletePolicy},
	}); err != nil {
		logger.Error(err, "failed to delete related jobs")
		return err
	}
	// Remove the finalizer from the subscription and update it.
	controllerutil.RemoveFinalizer(sub, subscriptionFinalizer)
	sub.Status.State = corev1alpha1.StateUninstalled
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) updateSubscription(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)
	if err := r.Update(ctx, sub); err != nil {
		logger.Error(err, "failed to update subscription")
		return err
	}
	// sync extension status
	if err := r.syncExtensionStatus(ctx, sub); err != nil {
		logger.Error(err, "failed sync extension status")
		return err
	}
	return nil
}

func createNamespace(ctx context.Context, client client.Client, namespace string) error {
	logger := klog.FromContext(ctx).WithValues("namespace", namespace)

	var ns corev1.Namespace
	err := client.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
	if errors.IsNotFound(err) {
		ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespace,
				Labels: map[string]string{tenantv1alpha1.WorkspaceLabel: systemWorkspace},
			},
		}
		if err := client.Create(ctx, &ns); err != nil {
			logger.Error(err, "failed to create namespace")
			return err
		}
	} else if err != nil {
		logger.Error(err, "failed to get namespace")
		return err
	}
	return nil
}

func createOrUpdateRoleBinding(ctx context.Context, client client.Client, namespace string, role rbacv1.Role, sa rbacv1.Subject) error {
	logger := klog.FromContext(ctx).WithValues("namespace", namespace, "role", defaultRole)
	var r rbacv1.Role
	err := client.Get(ctx, types.NamespacedName{Name: defaultRole, Namespace: namespace}, &r)
	if errors.IsNotFound(err) {
		r.Namespace = namespace
		r.Name = defaultRole
		r.Rules = role.Rules
		if err := client.Create(ctx, &r); err != nil {
			logger.Error(err, "failed to create role")
			return err
		}
	} else if err != nil {
		return err
	}

	if !reflect.DeepEqual(r.Rules, role.Rules) {
		expected := r.DeepCopy()
		expected.Rules = role.Rules
		if err := client.Update(ctx, expected); err != nil {
			logger.Error(err, "failed to update role")
			return err
		}
	}

	var rb rbacv1.RoleBinding
	err = client.Get(ctx, types.NamespacedName{Name: defaultRoleBinding, Namespace: namespace}, &rb)
	if errors.IsNotFound(err) {
		rb = rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultRoleBinding,
				Namespace: namespace,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     defaultRole,
			},
			Subjects: []rbacv1.Subject{
				sa,
			},
		}
		if err := client.Create(ctx, &rb); err != nil {
			logger.Error(err, "failed to create role binding")
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func initTargetNamespace(ctx context.Context, client client.Client, namespace string, clusterRole rbacv1.ClusterRole, role rbacv1.Role) error {
	logger := klog.FromContext(ctx).WithValues("namespace", namespace)
	if err := createNamespace(ctx, client, namespace); err != nil {
		logger.Error(err, "failed to create namespace")
		return err
	}
	sa := rbacv1.Subject{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      "default",
		Namespace: namespace,
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := createOrUpdateRoleBinding(ctx, client, namespace, role, sa); err != nil {
			return err
		}
		if err := createOrUpdateClusterRoleBinding(ctx, client, namespace, clusterRole, sa); err != nil {
			return err
		}
		return nil
	})
}

func createOrUpdateClusterRoleBinding(ctx context.Context, client client.Client, namespace string, clusterRole rbacv1.ClusterRole, sa rbacv1.Subject) error {
	defaultClusterRole := fmt.Sprintf(defaultClusterRoleFormat, namespace)
	defaultClusterRoleBinding := fmt.Sprintf(defaultClusterRoleBindingFormat, namespace)
	logger := klog.FromContext(ctx).WithValues("namespace", namespace, "cluster role", defaultClusterRole)
	var cr rbacv1.ClusterRole
	err := client.Get(ctx, types.NamespacedName{Name: defaultClusterRole, Namespace: namespace}, &cr)
	if errors.IsNotFound(err) {
		cr.Name = defaultClusterRole
		cr.Rules = clusterRole.Rules
		if err := client.Create(ctx, &cr); err != nil {
			logger.Error(err, "failed to create cluster role")
			return err
		}
	} else if err != nil {
		return err
	}

	if !reflect.DeepEqual(cr.Rules, clusterRole.Rules) {
		expected := cr.DeepCopy()
		expected.Rules = clusterRole.Rules
		if err := client.Update(ctx, expected); err != nil {
			logger.Error(err, "failed to update cluster role")
			return err
		}
	}

	var crb rbacv1.ClusterRoleBinding
	err = client.Get(ctx, types.NamespacedName{Name: defaultClusterRoleBinding, Namespace: namespace}, &crb)
	if errors.IsNotFound(err) {
		crb = rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: defaultClusterRoleBinding,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     defaultClusterRole,
			},
			Subjects: []rbacv1.Subject{
				sa,
			},
		}
		if err := client.Create(ctx, &crb); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func syncExtendedAPIStatus(ctx context.Context, clusterClient client.Client, sub *corev1alpha1.Subscription) error {
	jsBundles := &extensionsv1alpha1.JSBundleList{}
	if err := clusterClient.List(ctx, jsBundles, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return err
	}
	for _, item := range jsBundles.Items {
		if err := syncJSBundleStatus(ctx, clusterClient, sub, item); err != nil {
			return err
		}
	}

	apiServices := &extensionsv1alpha1.APIServiceList{}
	if err := clusterClient.List(ctx, apiServices, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return err
	}
	for _, item := range apiServices.Items {
		if err := syncAPIServiceStatus(ctx, clusterClient, sub, item); err != nil {
			return err
		}
	}

	reverseProxies := &extensionsv1alpha1.ReverseProxyList{}
	if err := clusterClient.List(ctx, reverseProxies, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return err
	}
	for _, item := range reverseProxies.Items {
		if err := syncReverseProxyStatus(ctx, clusterClient, sub, item); err != nil {
			return err
		}
	}

	return nil
}

func syncJSBundleStatus(ctx context.Context, clusterClient client.Client, sub *corev1alpha1.Subscription, jsBundle extensionsv1alpha1.JSBundle) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := clusterClient.Get(ctx, types.NamespacedName{Name: jsBundle.Name}, &jsBundle); err != nil {
			return err
		}
		// TODO unavailable state should be considered
		expected := jsBundle.DeepCopy()
		if sub.Spec.Enabled {
			expected.Status.State = extensionsv1alpha1.StateAvailable
		} else {
			expected.Status.State = extensionsv1alpha1.StateDisabled
		}
		if expected.Status.State != jsBundle.Status.State {
			if err := clusterClient.Update(ctx, expected); err != nil {
				return err
			}
		}
		return nil
	})
}

func syncAPIServiceStatus(ctx context.Context, clusterClient client.Client, sub *corev1alpha1.Subscription, apiService extensionsv1alpha1.APIService) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := clusterClient.Get(ctx, types.NamespacedName{Name: apiService.Name}, &apiService); err != nil {
			return err
		}
		// TODO unavailable state should be considered
		expected := apiService.DeepCopy()
		if sub.Spec.Enabled {
			expected.Status.State = extensionsv1alpha1.StateAvailable
		} else {
			expected.Status.State = extensionsv1alpha1.StateDisabled
		}
		if expected.Status.State != apiService.Status.State {
			if err := clusterClient.Update(ctx, expected); err != nil {
				return err
			}
		}
		return nil
	})
}

func syncReverseProxyStatus(ctx context.Context, clusterClient client.Client, sub *corev1alpha1.Subscription, reverseProxy extensionsv1alpha1.ReverseProxy) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := clusterClient.Get(ctx, types.NamespacedName{Name: reverseProxy.Name}, &reverseProxy); err != nil {
			return err
		}
		expected := reverseProxy.DeepCopy()
		if sub.Spec.Enabled {
			expected.Status.State = extensionsv1alpha1.StateAvailable
		} else {
			expected.Status.State = extensionsv1alpha1.StateDisabled
		}
		if expected.Status.State != reverseProxy.Status.State {
			if err := clusterClient.Update(ctx, expected); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SubscriptionReconciler) updateExtensionStatus(ctx context.Context, extensionName string, status corev1alpha1.ExtensionStatus) error {
	logger := klog.FromContext(ctx)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		extension := &corev1alpha1.Extension{}
		if err := r.Get(ctx, types.NamespacedName{Name: extensionName}, extension); err != nil {
			return client.IgnoreNotFound(err)
		}

		expected := extension.DeepCopy()
		expected.Status.State = status.State
		expected.Status.SubscribedVersion = status.SubscribedVersion

		if expected.Status.State != extension.Status.State ||
			expected.Status.SubscribedVersion != extension.Status.SubscribedVersion {
			if err := r.Update(ctx, expected); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to update subscription")
	}
	return nil
}

func (r *SubscriptionReconciler) syncExtensionStatus(ctx context.Context, sub *corev1alpha1.Subscription) error {
	expected := corev1alpha1.ExtensionStatus{}
	if sub.Status.State == corev1alpha1.StateUninstalled {
		expected.State = ""
		expected.SubscribedVersion = ""
	} else if sub.Status.State == corev1alpha1.StateInstalled {
		if sub.Spec.Enabled {
			expected.State = corev1alpha1.StateEnabled
		} else {
			expected.State = corev1alpha1.StateDisabled
		}
		expected.SubscribedVersion = sub.Spec.Extension.Version
	} else {
		expected.State = sub.Status.State
		expected.SubscribedVersion = sub.Spec.Extension.Version
	}
	return r.updateExtensionStatus(ctx, sub.Spec.Extension.Name, expected)
}

func (r *SubscriptionReconciler) syncClusterSchedulingStatus(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)
	// extension is already installed
	if sub.Status.State == corev1alpha1.StateInstalled {
		var targetClusters []clusterv1alpha1.Cluster
		if len(sub.Spec.ClusterScheduling.Placement.Clusters) > 0 {
			for _, target := range sub.Spec.ClusterScheduling.Placement.Clusters {
				var cluster clusterv1alpha1.Cluster
				if err := r.Get(ctx, types.NamespacedName{Name: target}, &cluster); err != nil {
					if errors.IsNotFound(err) {
						logger.V(4).Info("cluster not found")
						continue
					}
					return err
				}
				targetClusters = append(targetClusters, cluster)
			}
		} else if sub.Spec.ClusterScheduling.Placement.ClusterSelector != nil {
			clusterList := &clusterv1alpha1.ClusterList{}
			selector, _ := metav1.LabelSelectorAsSelector(sub.Spec.ClusterScheduling.Placement.ClusterSelector)
			if err := r.List(ctx, clusterList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
				return err
			}
			targetClusters = clusterList.Items
		}

		for _, cluster := range targetClusters {
			if err := r.syncClusterStatus(ctx, sub, cluster); err != nil {
				return err
			}
		}

		for clusterName, clusterSchedulingStatus := range sub.Status.ClusterSchedulingStatuses {
			if !hasCluster(targetClusters, clusterName) {
				if err := r.uninstall(ctx, sub, clusterName); err != nil {
					updateClusterSchedulingStateAndCondition(sub, clusterName, &clusterSchedulingStatus, corev1alpha1.StateUninstalled, "")
					if err := r.updateSubscription(ctx, sub); err != nil {
						logger.Error(err, "failed to update scheduling state and conditions")
						return err
					}
					return err
				}
			}
		}
	}
	return nil
}

func (r *SubscriptionReconciler) syncClusterStatus(ctx context.Context, sub *corev1alpha1.Subscription, cluster clusterv1alpha1.Cluster) error {
	clusterSchedulingStatus := sub.Status.ClusterSchedulingStatuses[cluster.Name]

	logger := klog.FromContext(ctx).WithValues("cluster", cluster.Name)

	switch clusterSchedulingStatus.State {
	case "":
		if err := r.installClusterAgent(ctx, sub, cluster, &clusterSchedulingStatus); err != nil {
			updateClusterSchedulingStateAndCondition(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateInstallFailed, err.Error())
			if err := r.updateSubscription(ctx, sub); err != nil {
				logger.Error(err, "failed to update subscription")
				return err
			}
			return nil
		}
	case corev1alpha1.StateInstalling, corev1alpha1.StateUpgrading:
		if err := r.syncSchedulingJobStatus(ctx, sub, cluster, &clusterSchedulingStatus); err != nil {
			return err
		}
	case corev1alpha1.StateInstalled: // installed -> enabled/disabled, enabled <-> disabled
		// cluster client without cache
		clusterClient, err := newClusterClient(cluster)
		if err != nil {
			klog.Errorf("failed to create cluster client: %v", err)
			return err
		}
		if err := syncExtendedAPIStatus(ctx, clusterClient, sub); err != nil {
			return err
		}
	case corev1alpha1.StateInstallFailed, corev1alpha1.StateUninstallFailed:
		// The installation/uninstallation has failed, so do nothing
		break
	}
	return nil
}

// newClusterClient returns controller runtime client without cache
func newClusterClient(cluster clusterv1alpha1.Cluster) (client.Client, error) {
	bytes, err := clientcmd.NewClientConfigFromBytes(cluster.Spec.Connection.KubeConfig)
	if err != nil {
		return nil, err
	}
	config, err := bytes.ClientConfig()
	if err != nil {
		return nil, err
	}
	return client.New(config, client.Options{})
}

func (r *SubscriptionReconciler) installClusterAgent(ctx context.Context, sub *corev1alpha1.Subscription, cluster clusterv1alpha1.Cluster, clusterSchedulingStatus *corev1alpha1.InstallationStatus) error {
	logger := klog.FromContext(ctx)
	updateClusterSchedulingStateAndCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StatePreparing, "")
	if err := r.updateSubscription(ctx, sub); err != nil {
		logger.Error(err, "failed to update scheduling state and conditions")
		return err
	}

	// cluster client without cache
	clusterClient, err := newClusterClient(cluster)
	if err != nil {
		logger.Error(err, "failed to create cluster client")
		return err
	}

	charData, _, err := r.loadChartData(ctx, &sub.Spec.Extension)
	if err != nil {
		logger.Error(err, "failed to load chart data")
		return err
	}

	clusterRole, role := usesPermissions(charData)

	// TODO: reconsider how to define the target namespace
	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name
	if err := initTargetNamespace(ctx, clusterClient, targetNamespace, clusterRole, role); err != nil {
		logger.WithValues("namespace", targetNamespace).Error(err, "failed to init target namespace")
		return err
	}

	options := r.defaultHelmOptions()
	options = append(options, helm.SetLabels(map[string]string{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}))

	// create executor in the host cluster
	helmExecutor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		logger.Error(err, "failed to create helm executor")
		return err
	}

	helmOptions := []helm.HelmOption{helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)),
		helm.SetKubeAsUser(fmt.Sprintf("system:serviceaccount:%s:default", targetNamespace)),
		helm.SetOverrides([]string{"tags.agent=true", "agent.enabled=true"}),
		helm.SetHelmJobLabels(map[string]string{
			corev1alpha1.SubscriptionReferenceLabel: sub.Name,
		})}

	var jobName string
	if _, err = helmExecutor.Manifest(helmOptions...); err != nil {
		// release not exists or there is something wrong with the Manifest API
		if !strings.Contains(err.Error(), "release: not found") {
			return err
		}
		logger.V(4).Info("install helm release", "namespace", targetNamespace, "release", releaseName)
		jobName, err = helmExecutor.Install(ctx, sub.Spec.Extension.Name, charData, []byte(clusterConfig(sub, cluster.Name)), helmOptions...)
		if err != nil {
			logger.Error(err, "failed to install helm release")
			return err
		}
	} else {
		// release exists, we need to upgrade it
		logger.V(4).Info("upgrade helm release", "namespace", targetNamespace, "release", releaseName)
		jobName, err = helmExecutor.Upgrade(ctx, sub.Spec.Extension.Name, charData, []byte(clusterConfig(sub, cluster.Name)), helmOptions...)
		if err != nil {
			logger.Error(err, "failed to upgrade helm release")
			return err
		}
	}
	clusterSchedulingStatus.ReleaseName = releaseName
	clusterSchedulingStatus.TargetNamespace = targetNamespace
	clusterSchedulingStatus.JobName = jobName
	updateClusterSchedulingStateAndCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstalling, "")
	if err := r.updateSubscription(ctx, sub); err != nil {
		logger.Error(err, "failed to update scheduling state and conditions")
		return err
	}
	return nil
}

func (r *SubscriptionReconciler) syncSubscriptionStatus(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)

	switch sub.Status.State {
	case "":
		// preparing -> installing
		if err := r.installExtension(ctx, sub); err != nil {
			logger.Error(err, "failed to install extension")
			updateStateAndCondition(sub, corev1alpha1.StateInstallFailed, err.Error())
			if err := r.updateSubscription(ctx, sub); err != nil {
				return err
			}
			return nil
		}
	case corev1alpha1.StateInstalling, corev1alpha1.StateUpgrading:
		// installing -> installed or install failed
		if err := r.syncExecutorJobStatus(ctx, sub); err != nil {
			return err
		}
	case corev1alpha1.StateInstalled, corev1alpha1.StateEnabled, corev1alpha1.StateDisabled:
		// installed -> enabled/disabled
		// enabled <-> disabled
		if err := syncExtendedAPIStatus(ctx, r.Client, sub); err != nil {
			return err
		}
		if err := r.syncExtensionStatus(ctx, sub); err != nil {
			return err
		}
	case corev1alpha1.StatePreparing, corev1alpha1.StateInstallFailed, corev1alpha1.StateUninstallFailed:
		// The installation/uninstallation has failed, so do nothing
		break
	}

	return nil
}

func (r *SubscriptionReconciler) uninstall(ctx context.Context, sub *corev1alpha1.Subscription, clusterName string) error {
	logger := klog.FromContext(ctx)
	var cluster clusterv1alpha1.Cluster
	if err := r.Get(ctx, types.NamespacedName{Name: clusterName}, &cluster); err != nil {
		if errors.IsNotFound(err) {
			logger.V(4).Info("cluster not found")
			delete(sub.Status.ClusterSchedulingStatuses, clusterName)
			if err := r.updateSubscription(ctx, sub); err != nil {
				logger.Error(err, "failed to update scheduling state and conditions")
				return err
			}
			return nil
		}
		return err
	}

	clusterSchedulingStatus := sub.Status.ClusterSchedulingStatuses[clusterName]

	if clusterSchedulingStatus.State == corev1alpha1.StateUninstalling {
		if clusterSchedulingStatus.JobName == "" {
			// This is unlikely to happen in normal processes, and this is just to avoid subsequent exceptions
			return nil
		}
		job := batchv1.Job{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: clusterSchedulingStatus.JobName}, &job); err != nil {
			return err
		}
		completed, failed := jobStatus(job)
		if completed {
			delete(sub.Status.ClusterSchedulingStatuses, clusterName)
			if err := r.updateSubscription(ctx, sub); err != nil {
				logger.Error(err, "failed to update scheduling state and conditions")
				return err
			}
		} else if failed {
			updateClusterSchedulingStateAndCondition(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateUninstallFailed, latestJobCondition(job).Message)
			if err := r.updateSubscription(ctx, sub); err != nil {
				logger.Error(err, "failed to update subscription")
				return err
			}
		}
	} else {
		helmExecutor, err := helm.NewExecutor(r.kubeconfig, sub.Status.TargetNamespace, sub.Status.ReleaseName, r.defaultHelmOptions()...)
		if err != nil {
			logger.Error(err, "failed to create helm executor")
			return err
		}

		helmOptions := []helm.HelmOption{helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)),
			helm.SetKubeAsUser(fmt.Sprintf("system:serviceaccount:%s:default", fmt.Errorf(targetNamespaceFormat, sub.Spec.Extension.Name))),
			helm.SetHelmJobLabels(map[string]string{
				corev1alpha1.SubscriptionReferenceLabel: sub.Name,
			})}

		jobName, err := helmExecutor.Uninstall(ctx, helmOptions...)
		if err != nil {
			logger.Error(err, "failed to uninstall helm relase")
			return err
		}

		clusterSchedulingStatus.JobName = jobName
		updateClusterSchedulingStateAndCondition(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateUninstalling, "")
		if err := r.updateSubscription(ctx, sub); err != nil {
			logger.Error(err, "failed to update scheduling state and conditions")
			return err
		}
	}

	return nil

}
