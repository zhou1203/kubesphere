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
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/getter"
	helmrelease "helm.sh/helm/v3/pkg/release"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
	agentReleaseFormat              = "%s-agent"
	defaultRole                     = "kubesphere:helm-executor"
	defaultRoleBinding              = "kubesphere:helm-executor"
	defaultClusterRoleFormat        = "kubesphere:%s:helm-executor"
	permissionDefinitionFile        = "permissions.yaml"
	defaultClusterRoleBindingFormat = defaultClusterRoleFormat
)

var _ reconcile.Reconciler = &SubscriptionReconciler{}

// SubscriptionReconciler reconciles a Subscription object.
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
			logger.Error(err, "failed to sync scheduling status")
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

// reconcileDelete delete the helm release involved and remove finalizer from subscription.
func (r *SubscriptionReconciler) reconcileDelete(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	logger := klog.FromContext(ctx)

	// It has not been installed correctly.
	if sub.Status.ReleaseName == "" {
		if err := r.postRemove(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	if len(sub.Status.ClusterSchedulingStatuses) > 0 {
		for clusterName, clusterSchedulingStatus := range sub.Status.ClusterSchedulingStatuses {
			if err := r.uninstallClusterAgent(ctx, sub, clusterName); err != nil {
				updateClusterSchedulingState(sub, clusterName, &clusterSchedulingStatus, corev1alpha1.StateUninstalled)
				updateClusterSchedulingCondition(sub, clusterName, &clusterSchedulingStatus, corev1alpha1.ConditionTypeUninstalled, err.Error(), metav1.ConditionFalse)
				if err = r.updateSubscription(ctx, sub); err != nil {
					logger.Error(err, "failed to update scheduling state and conditions")
					return ctrl.Result{}, err
				}
			}
		}
		return ctrl.Result{}, nil
	}

	helmExecutor, err := helm.NewExecutor(r.kubeconfig, sub.Status.TargetNamespace, sub.Status.ReleaseName,
		helm.SetJobLabels(map[string]string{corev1alpha1.SubscriptionReferenceLabel: sub.Name}))
	if err != nil {
		logger.Error(err, "failed to create helm executor")
		return ctrl.Result{}, err
	}

	if sub.Annotations[corev1alpha1.ForceDeleteAnnotation] == "true" {
		if err = helmExecutor.ForceDelete(ctx); err != nil {
			return ctrl.Result{}, err
		}
		if err = r.postRemove(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	if sub.Status.State != corev1alpha1.StateUninstalling {
		jobName, err := helmExecutor.Uninstall(ctx)
		if err != nil {
			logger.Error(err, "failed to delete helm release")
			updateState(sub, corev1alpha1.StateUninstallFailed)
			updateCondition(sub, corev1alpha1.ConditionTypeUninstalled, err.Error(), metav1.ConditionFalse)
			if err := r.updateSubscription(ctx, sub); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		sub.Status.JobName = jobName
		updateState(sub, corev1alpha1.StateUninstalling)
		if err := r.updateSubscription(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	if _, err = helmExecutor.Release(); err != nil {
		if isReleaseNotFoundError(err) {
			// The involved release does not exist, just move on.
			return ctrl.Result{}, r.postRemove(ctx, sub)
		}
		return ctrl.Result{}, err
	}

	if sub.Status.State == corev1alpha1.StateUninstalling {
		job := batchv1.Job{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: sub.Status.JobName}, &job); err != nil {
			return ctrl.Result{}, err
		}

		active, completed, failed := jobStatus(job)
		if active {
			return ctrl.Result{}, nil
		}

		if completed {
			klog.V(4).Infof("remove the finalizer for subscription %s", sub.Name)
			if err = r.postRemove(ctx, sub); err != nil {
				return ctrl.Result{}, err
			}
		} else if failed {
			updateState(sub, corev1alpha1.StateUninstallFailed)
			updateCondition(sub, corev1alpha1.ConditionTypeUninstalled, latestJobCondition(job).Message, metav1.ConditionFalse)
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

func jobStatus(job batchv1.Job) (active, completed, failed bool) {
	active = job.Status.Active > 0
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

func updateState(sub *corev1alpha1.Subscription, state string) {
	sub.Status.State = state

	newState := corev1alpha1.SubscriptionState{
		LastTransitionTime: metav1.Now(),
		State:              state,
	}

	if sub.Status.StateHistory == nil {
		sub.Status.StateHistory = []corev1alpha1.SubscriptionState{newState}
		return
	}

	// We need to limit the number of StateHistory, and if the limit is exceeded, delete the oldest one.

	// Not exceeding the limit
	if len(sub.Status.StateHistory) < corev1alpha1.MaxStateNum {
		sub.Status.StateHistory = append(sub.Status.StateHistory, newState)
		return
	}

	sort.Slice(sub.Status.StateHistory, func(i, j int) bool {
		return sub.Status.StateHistory[i].LastTransitionTime.After(sub.Status.StateHistory[j].LastTransitionTime.Time)
	})
	sub.Status.StateHistory = append(sub.Status.StateHistory[:corev1alpha1.MaxStateNum-1], newState)
}

func updateCondition(sub *corev1alpha1.Subscription, conditionType, message string, status metav1.ConditionStatus) {
	conditions := []metav1.Condition{
		{
			Type:               conditionType,
			Reason:             conditionType,
			Status:             status,
			LastTransitionTime: metav1.Now(),
			Message:            message,
		},
	}
	if len(sub.Status.Conditions) == 0 {
		sub.Status.Conditions = conditions
		return
	}

	for _, c := range sub.Status.Conditions {
		if c.Type != conditionType {
			conditions = append(conditions, c)
		}
	}
	sub.Status.Conditions = conditions
}

func updateClusterSchedulingState(sub *corev1alpha1.Subscription, clusterName string, clusterSchedulingStatus *corev1alpha1.InstallationStatus, state string) {
	clusterSchedulingStatus.State = state

	newState := corev1alpha1.SubscriptionState{
		LastTransitionTime: metav1.Now(),
		State:              state,
	}

	if sub.Status.ClusterSchedulingStatuses == nil {
		sub.Status.ClusterSchedulingStatuses = make(map[string]corev1alpha1.InstallationStatus)
	}

	if clusterSchedulingStatus.StateHistory == nil {
		clusterSchedulingStatus.StateHistory = []corev1alpha1.SubscriptionState{newState}
		sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
		return
	}

	// We need to limit the number of StateHistory, and if the limit is exceeded, delete the oldest one.

	// Not exceeding the limit
	if len(clusterSchedulingStatus.StateHistory) < corev1alpha1.MaxStateNum {
		clusterSchedulingStatus.StateHistory = append(clusterSchedulingStatus.StateHistory, newState)
		sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
		return
	}

	sort.Slice(clusterSchedulingStatus.StateHistory, func(i, j int) bool {
		return clusterSchedulingStatus.StateHistory[i].LastTransitionTime.After(clusterSchedulingStatus.StateHistory[j].LastTransitionTime.Time)
	})
	clusterSchedulingStatus.StateHistory = append(clusterSchedulingStatus.StateHistory[:corev1alpha1.MaxStateNum-1], newState)
	sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
}

func updateClusterSchedulingCondition(
	sub *corev1alpha1.Subscription, clusterName string, clusterSchedulingStatus *corev1alpha1.InstallationStatus,
	conditionType, message string, status metav1.ConditionStatus,
) {
	if sub.Status.ClusterSchedulingStatuses == nil {
		sub.Status.ClusterSchedulingStatuses = make(map[string]corev1alpha1.InstallationStatus)
	}

	conditions := []metav1.Condition{
		{
			Type:               conditionType,
			Reason:             conditionType,
			Status:             status,
			LastTransitionTime: metav1.Now(),
			Message:            message,
		},
	}
	if len(clusterSchedulingStatus.Conditions) == 0 {
		clusterSchedulingStatus.Conditions = conditions
		sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
		return
	}

	for _, c := range clusterSchedulingStatus.Conditions {
		if c.Type != conditionType {
			conditions = append(conditions, c)
		}
	}
	clusterSchedulingStatus.Conditions = conditions
	sub.Status.ClusterSchedulingStatuses[clusterName] = *clusterSchedulingStatus
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
	updateCondition(sub, corev1alpha1.ConditionTypeUninstalled, "", metav1.ConditionTrue)
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

func createNamespaceIfNotExists(ctx context.Context, client client.Client, namespace string) error {
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
	if err := createNamespaceIfNotExists(ctx, client, namespace); err != nil {
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
	if sub.Status.State != corev1alpha1.StateInstalled {
		return nil
	}
	// extension is already installed
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

	for clusterName := range sub.Status.ClusterSchedulingStatuses {
		if !hasCluster(targetClusters, clusterName) {
			if err := r.uninstallClusterAgent(ctx, sub, clusterName); err != nil {
				return err
			}
		}
	}

	return nil
}

// syncSubscriptionStatus syncs the installation status of an extension.
func (r *SubscriptionReconciler) syncSubscriptionStatus(ctx context.Context, sub *corev1alpha1.Subscription) error {
	logger := klog.FromContext(ctx)

	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name
	options := []helm.ExecutorOption{helm.SetJobLabels(map[string]string{corev1alpha1.SubscriptionReferenceLabel: sub.Name})}

	executor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		logger.Error(err, "failed to create executor")
		return err
	}

	switch sub.Status.State {
	case "": // Install the Subscription
		// Check if the target helm release exists.
		// If it does, there is no need to execute the installation process again.
		release, err := executor.Release()
		if err == nil {
			// Has been installed successfully or failed
			switch release.Info.Status {
			case helmrelease.StatusFailed:
				updateState(sub, corev1alpha1.StateInstallFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, release.Info.Description, metav1.ConditionFalse)
				return r.updateSubscription(ctx, sub)
			case helmrelease.StatusDeployed:
				updateState(sub, corev1alpha1.StateInstalled)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, "", metav1.ConditionTrue)
				return r.updateSubscription(ctx, sub)
			default:
				return nil
			}
		}

		if !isReleaseNotFoundError(err) {
			logger.Error(err, "failed to get helm release status")
			return err
		}
		return r.installOrUpgradeExtension(ctx, sub, executor, false)
	case corev1alpha1.StateInstalling:
		return r.syncExtensionInstallationStatus(ctx, sub, false)
	case corev1alpha1.StateUpgrading:
		return r.syncExtensionInstallationStatus(ctx, sub, true)
	case corev1alpha1.StateInstalled:
		// upgrade after configuration changes
		if configChanged(sub, "") || r.versionChanged(ctx, sub) {
			return r.installOrUpgradeExtension(ctx, sub, executor, true)
		}

		if err = syncExtendedAPIStatus(ctx, r.Client, sub); err != nil {
			return err
		}
		if err = r.syncExtensionStatus(ctx, sub); err != nil {
			return err
		}
		return r.updateReadyCondition(ctx, sub, executor)
	default: // InstallFailed
		return nil
	}
}

func needUpdateReadyCondition(conditions []metav1.Condition, ready bool) bool {
	for _, condition := range conditions {
		if condition.Type != corev1alpha1.ConditionTypeReady {
			continue
		}
		// Status does not need to be updated
		if (ready && condition.Status == metav1.ConditionTrue) || (!ready && condition.Status == metav1.ConditionFalse) {
			return false
		}
	}
	return true
}

func (r *SubscriptionReconciler) updateReadyCondition(ctx context.Context, sub *corev1alpha1.Subscription, executor helm.Executor) error {
	ready, err := executor.IsReleaseReady(time.Second * 30)

	if !needUpdateReadyCondition(sub.Status.Conditions, ready) {
		return nil
	}

	if ready {
		updateCondition(sub, corev1alpha1.ConditionTypeReady, "", metav1.ConditionTrue)
	} else {
		updateCondition(sub, corev1alpha1.ConditionTypeReady, err.Error(), metav1.ConditionFalse)
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) syncClusterStatus(ctx context.Context, sub *corev1alpha1.Subscription, cluster clusterv1alpha1.Cluster) error {
	logger := klog.FromContext(ctx).WithValues("cluster", cluster.Name)
	clusterSchedulingStatus := sub.Status.ClusterSchedulingStatuses[cluster.Name]

	// cluster client without cache
	clusterClient, err := newClusterClient(cluster)
	if err != nil {
		logger.Error(err, "failed to create cluster client")
		return err
	}

	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := fmt.Sprintf(agentReleaseFormat, sub.Spec.Extension.Name)

	options := []helm.ExecutorOption{helm.SetJobLabels(map[string]string{corev1alpha1.SubscriptionReferenceLabel: sub.Name})}

	executor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		logger.Error(err, "failed to create executor")
		return err
	}

	switch clusterSchedulingStatus.State {
	case "":
		release, err := executor.Release(helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)))
		if err == nil {
			// Has been installed successfully or failed
			switch release.Info.Status {
			case helmrelease.StatusFailed:
				updateClusterSchedulingState(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateInstallFailed)
				updateClusterSchedulingCondition(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.ConditionTypeInstalled, release.Info.Description, metav1.ConditionFalse)
				return r.updateSubscription(ctx, sub)
			case helmrelease.StatusDeployed:
				updateClusterSchedulingState(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateInstalled)
				updateClusterSchedulingCondition(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.ConditionTypeInstalled, "", metav1.ConditionTrue)
				return r.updateSubscription(ctx, sub)
			default:
				return nil
			}
		}

		if !isReleaseNotFoundError(err) {
			logger.Error(err, "failed to get helm release status")
			return err
		}
		return r.installOrUpgradeClusterAgent(ctx, sub, cluster, clusterClient, &clusterSchedulingStatus, executor, false)
	case corev1alpha1.StateInstalling:
		return r.syncClusterAgentInstallationStatus(ctx, sub, cluster, &clusterSchedulingStatus, false)
	case corev1alpha1.StateUpgrading:
		return r.syncClusterAgentInstallationStatus(ctx, sub, cluster, &clusterSchedulingStatus, true)
	case corev1alpha1.StateInstalled:
		// upgrade after configuration changes
		if configChanged(sub, cluster.Name) || r.versionChanged(ctx, sub) {
			return r.installOrUpgradeClusterAgent(ctx, sub, cluster, clusterClient, &clusterSchedulingStatus, executor, true)
		}

		if err = syncExtendedAPIStatus(ctx, clusterClient, sub); err != nil {
			return err
		}
		return r.updateClusterReadyCondition(ctx, sub, executor, cluster.Name, &clusterSchedulingStatus)
	default: // InstallFailed
		return nil
	}
}

func (r *SubscriptionReconciler) updateClusterReadyCondition(ctx context.Context, sub *corev1alpha1.Subscription, executor helm.Executor, clusterName string, clusterSchedulingStatus *corev1alpha1.InstallationStatus) error {
	ready, err := executor.IsReleaseReady(time.Second * 30)

	if !needUpdateReadyCondition(clusterSchedulingStatus.Conditions, ready) {
		return nil
	}

	if ready {
		updateClusterSchedulingCondition(sub, clusterName, clusterSchedulingStatus, corev1alpha1.ConditionTypeReady, "", metav1.ConditionTrue)
	} else {
		updateClusterSchedulingCondition(sub, clusterName, clusterSchedulingStatus, corev1alpha1.ConditionTypeReady, err.Error(), metav1.ConditionFalse)
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) installOrUpgradeExtension(ctx context.Context, sub *corev1alpha1.Subscription, executor helm.Executor, upgrade bool) error {
	logger := klog.FromContext(ctx)

	updateState(sub, corev1alpha1.StatePreparing)
	updateCondition(sub, corev1alpha1.ConditionTypeInitialized, "", metav1.ConditionTrue)
	if err := r.updateSubscription(ctx, sub); err != nil {
		return err
	}

	charData, extensionVersion, err := r.loadChartData(ctx, &sub.Spec.Extension)
	if err != nil {
		logger.Error(err, "failed to load chart data")
		if !isServerSideError(err) {
			if upgrade {
				updateState(sub, corev1alpha1.StateUpgradeFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeUpgraded, err.Error(), metav1.ConditionFalse)
			} else {
				updateState(sub, corev1alpha1.StateInstallFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, err.Error(), metav1.ConditionFalse)
			}
			return r.updateSubscription(ctx, sub)
		}
		return err
	}

	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name

	if !upgrade {
		clusterRole, role := usesPermissions(charData)

		if err = initTargetNamespace(ctx, r.Client, targetNamespace, clusterRole, role); err != nil {
			logger.Error(err, "failed to init target namespace", "namespace", targetNamespace)
			if !isServerSideError(err) {
				updateState(sub, corev1alpha1.StateInstallFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, err.Error(), metav1.ConditionFalse)
				return r.updateSubscription(ctx, sub)
			}
			return err
		}
	}

	options := []helm.HelmOption{
		helm.SetInstall(true),
		helm.SetLabels(map[string]string{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}),
	}
	if extensionVersion.Spec.InstallationMode == corev1alpha1.InstallationMulticluster {
		options = append(options, helm.SetOverrides([]string{"tags.extension=true", "extension.enabled=true"}))
	}

	jobName, err := executor.Upgrade(ctx, releaseName, charData, []byte(sub.Spec.Config), options...)
	if err != nil {
		logger.Error(err, "failed to create executor job", "namespace", targetNamespace)
		if !isServerSideError(err) {
			if upgrade {
				updateState(sub, corev1alpha1.StateUpgradeFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeUpgraded, err.Error(), metav1.ConditionFalse)
			} else {
				updateState(sub, corev1alpha1.StateInstallFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, err.Error(), metav1.ConditionFalse)
			}
			return r.updateSubscription(ctx, sub)
		}
		return err
	}

	setConfigHash(sub, "")
	sub.Status.ReleaseName = releaseName
	sub.Status.TargetNamespace = targetNamespace
	sub.Status.JobName = jobName
	if upgrade {
		updateState(sub, corev1alpha1.StateUpgrading)
	} else {
		updateState(sub, corev1alpha1.StateInstalling)
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) installOrUpgradeClusterAgent(ctx context.Context, sub *corev1alpha1.Subscription,
	cluster clusterv1alpha1.Cluster, clusterClient client.Client, clusterSchedulingStatus *corev1alpha1.InstallationStatus,
	executor helm.Executor, upgrade bool) error {
	logger := klog.FromContext(ctx)

	updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StatePreparing)
	updateClusterSchedulingCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeInitialized, "", metav1.ConditionTrue)
	if err := r.updateSubscription(ctx, sub); err != nil {
		return err
	}

	charData, _, err := r.loadChartData(ctx, &sub.Spec.Extension)
	if err != nil {
		logger.Error(err, "failed to load chart data")
		return err
	}

	targetNamespace := fmt.Sprintf(targetNamespaceFormat, sub.Spec.Extension.Name)
	releaseName := fmt.Sprintf(agentReleaseFormat, sub.Spec.Extension.Name)

	if !upgrade {
		clusterRole, role := usesPermissions(charData)

		if err = initTargetNamespace(ctx, clusterClient, targetNamespace, clusterRole, role); err != nil {
			logger.WithValues("namespace", targetNamespace).Error(err, "failed to init target namespace")
			return err
		}
	}

	helmOptions := []helm.HelmOption{helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)),
		helm.SetInstall(true),
		helm.SetKubeAsUser(fmt.Sprintf("system:serviceaccount:%s:default", targetNamespace)),
		helm.SetOverrides([]string{"tags.agent=true", "agent.enabled=true"})}

	jobName, err := executor.Upgrade(ctx, releaseName, charData, []byte(clusterConfig(sub, cluster.Name)), helmOptions...)
	if err != nil {
		logger.Error(err, "failed to upgrade helm release")
		return err
	}

	setConfigHash(sub, cluster.Name)
	clusterSchedulingStatus.ReleaseName = releaseName
	clusterSchedulingStatus.TargetNamespace = targetNamespace
	clusterSchedulingStatus.JobName = jobName
	if upgrade {
		updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateUpgrading)
	} else {
		updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstalling)
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) uninstallClusterAgent(ctx context.Context, sub *corev1alpha1.Subscription, clusterName string) error {
	logger := klog.FromContext(ctx).WithValues("cluster", clusterName)

	var cluster clusterv1alpha1.Cluster
	if err := r.Get(ctx, types.NamespacedName{Name: clusterName}, &cluster); err != nil {
		if errors.IsNotFound(err) {
			logger.V(4).Info("cluster not found")
			delete(sub.Status.ClusterSchedulingStatuses, clusterName)
			return r.updateSubscription(ctx, sub)
		}
		return err
	}

	clusterSchedulingStatus := sub.Status.ClusterSchedulingStatuses[clusterName]

	var (
		jobActive = false
		jobFailed = false
	)

	if clusterSchedulingStatus.State == corev1alpha1.StateUninstalling && clusterSchedulingStatus.JobName != "" {
		job := batchv1.Job{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: clusterSchedulingStatus.TargetNamespace, Name: clusterSchedulingStatus.JobName}, &job); err != nil {
			logger.Error(err, "failed to get job", "namespace", clusterSchedulingStatus.TargetNamespace, "job", clusterSchedulingStatus.JobName)
			return err
		}
		jobActive, _, jobFailed = jobStatus(job)

		if jobFailed && clusterSchedulingStatus.State != corev1alpha1.StateUninstallFailed {
			updateClusterSchedulingState(sub, clusterName, &clusterSchedulingStatus, corev1alpha1.StateUninstallFailed)
			updateClusterSchedulingCondition(
				sub, clusterName, &clusterSchedulingStatus, corev1alpha1.ConditionTypeUninstalled,
				latestJobCondition(job).Message, metav1.ConditionFalse,
			)
			if err := r.updateSubscription(ctx, sub); err != nil {
				return err
			}
			return nil
		}
	}

	// helm executor is still running
	if jobActive || jobFailed {
		return nil
	}

	targetNamespace := clusterSchedulingStatus.TargetNamespace
	releaseName := clusterSchedulingStatus.ReleaseName

	options := []helm.ExecutorOption{
		helm.SetJobLabels(map[string]string{corev1alpha1.SubscriptionReferenceLabel: sub.Name}),
	}

	executor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		logger.Error(err, "failed to create executor")
		return err
	}

	_, err = executor.Release(helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)))
	if err != nil {
		if isReleaseNotFoundError(err) {
			logger.V(4).Info("cluster not found")
			delete(sub.Status.ClusterSchedulingStatuses, clusterName)
			return r.updateSubscription(ctx, sub)
		}
		logger.Error(err, "failed to get helm release status")
		return err
	} else {
		jobName, err := executor.Uninstall(ctx, helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)))
		if err != nil {
			logger.Error(err, "failed to uninstall helm release")
			return err
		}

		clusterSchedulingStatus.JobName = jobName
		updateClusterSchedulingState(sub, cluster.Name, &clusterSchedulingStatus, corev1alpha1.StateUninstalling)
		if err := r.updateSubscription(ctx, sub); err != nil {
			logger.Error(err, "failed to update scheduling state and conditions")
			return err
		}
	}

	return nil
}

func (r *SubscriptionReconciler) syncExtensionInstallationStatus(ctx context.Context, sub *corev1alpha1.Subscription, upgrade bool) error {
	logger := klog.FromContext(ctx)
	if sub.Status.TargetNamespace == "" || sub.Status.JobName == "" {
		return nil
	}

	lastExecutorJob := batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: sub.Status.JobName}, &lastExecutorJob); err != nil {
		if errors.IsNotFound(err) {
			if upgrade {
				updateState(sub, corev1alpha1.StateUpgradeFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeUpgraded, fmt.Sprintf("helm executor job not found: %s", err.Error()), metav1.ConditionFalse)
			} else {
				updateState(sub, corev1alpha1.StateInstallFailed)
				updateCondition(sub, corev1alpha1.ConditionTypeInstalled, fmt.Sprintf("helm executor job not found: %s", err.Error()), metav1.ConditionFalse)
			}
			return r.updateSubscription(ctx, sub)
		}
		logger.Error(err, "failed to get job", "namespace", sub.Status.TargetNamespace, "job", sub.Status.JobName)
		return err
	}

	jobActive, jobCompleted, jobFailed := jobStatus(lastExecutorJob)
	if jobActive {
		return nil
	}

	if jobCompleted {
		updateState(sub, corev1alpha1.StateInstalled)
		if upgrade {
			updateCondition(sub, corev1alpha1.ConditionTypeUpgraded, "", metav1.ConditionTrue)
		} else {
			updateCondition(sub, corev1alpha1.ConditionTypeInstalled, "", metav1.ConditionTrue)
		}
		return r.updateSubscription(ctx, sub)
	}

	if jobFailed {
		if upgrade {
			updateState(sub, corev1alpha1.StateUpgradeFailed)
			updateCondition(sub, corev1alpha1.ConditionTypeUpgraded, latestJobCondition(lastExecutorJob).Message, metav1.ConditionFalse)
		} else {
			updateState(sub, corev1alpha1.StateInstallFailed)
			updateCondition(sub, corev1alpha1.ConditionTypeInstalled, latestJobCondition(lastExecutorJob).Message, metav1.ConditionFalse)
		}
		return r.updateSubscription(ctx, sub)
	}

	return nil
}

func (r *SubscriptionReconciler) syncClusterAgentInstallationStatus(
	ctx context.Context, sub *corev1alpha1.Subscription, cluster clusterv1alpha1.Cluster,
	clusterSchedulingStatus *corev1alpha1.InstallationStatus, upgrade bool,
) error {
	logger := klog.FromContext(ctx)

	if clusterSchedulingStatus.TargetNamespace == "" || clusterSchedulingStatus.JobName == "" {
		return nil
	}

	lastExecutorJob := batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: clusterSchedulingStatus.TargetNamespace, Name: clusterSchedulingStatus.JobName}, &lastExecutorJob); err != nil {
		if errors.IsNotFound(err) {
			if upgrade {
				updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateUpgradeFailed)
				updateClusterSchedulingCondition(
					sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeUpgraded,
					fmt.Sprintf("helm executor job not found: %s", err.Error()), metav1.ConditionFalse,
				)
			} else {
				updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstallFailed)
				updateClusterSchedulingCondition(
					sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeInstalled,
					fmt.Sprintf("helm executor job not found: %s", err.Error()), metav1.ConditionFalse,
				)
			}
			return r.updateSubscription(ctx, sub)
		}
		logger.Error(err, "failed to get job", "namespace", clusterSchedulingStatus.TargetNamespace, "job", clusterSchedulingStatus.JobName)
		return err
	}

	jobActive, jobCompleted, jobFailed := jobStatus(lastExecutorJob)

	if jobActive {
		return nil
	}

	if jobCompleted {
		updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstalled)
		if upgrade {
			updateClusterSchedulingCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeUpgraded, "", metav1.ConditionTrue)
		} else {
			updateClusterSchedulingCondition(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeInstalled, "", metav1.ConditionTrue)
		}
		return r.updateSubscription(ctx, sub)
	}

	if jobFailed {
		if upgrade {
			updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateUpgradeFailed)
			updateClusterSchedulingCondition(
				sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeUpgraded,
				latestJobCondition(lastExecutorJob).Message, metav1.ConditionFalse,
			)
		} else {
			updateClusterSchedulingState(sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.StateInstallFailed)
			updateClusterSchedulingCondition(
				sub, cluster.Name, clusterSchedulingStatus, corev1alpha1.ConditionTypeInstalled,
				latestJobCondition(lastExecutorJob).Message, metav1.ConditionFalse,
			)
		}
		return r.updateSubscription(ctx, sub)
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

func (r *SubscriptionReconciler) versionChanged(ctx context.Context, sub *corev1alpha1.Subscription) bool {
	extension := &corev1alpha1.Extension{}
	if err := r.Get(ctx, types.NamespacedName{Name: sub.Spec.Extension.Name}, extension); err != nil {
		klog.Warningf("get Extension %s failed: %v", sub.Spec.Extension.Name, err)
		return false
	}
	return sub.Spec.Extension.Version != extension.Status.SubscribedVersion
}
