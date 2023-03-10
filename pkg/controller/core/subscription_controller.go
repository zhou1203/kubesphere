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
	"io/ioutil"
	"sort"
	"strings"
	"time"

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
	SubscriptionFinalizer = "subscriptions.kubesphere.io"
	SystemWorkspace       = "system-workspace"
	HelmExecutor          = "kubesphere:helm-executor"
)

var _ reconcile.Reconciler = &SubscriptionReconciler{}

type SubscriptionReconciler struct {
	client.Client
	kubeconfig string
	helmGetter getter.Getter
	Recorder   record.EventRecorder
}

func NewSubscriptionReconciler(kubeconfigPath string) (*SubscriptionReconciler, error) {
	// TODO support more options (e.g. skipTLSVerify or basic auth etc.) for the specified repository
	helmGetter, _ := getter.NewHTTPGetter()

	var kubeconfig string
	if kubeconfigPath != "" {
		data, err := ioutil.ReadFile(kubeconfigPath)
		if err != nil {
			return nil, err
		}
		kubeconfig = string(data)
	}

	return &SubscriptionReconciler{kubeconfig: kubeconfig, helmGetter: helmGetter}, nil
}

func (r *SubscriptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(4).Infof("sync subscription: %s ", req.String())

	sub := &corev1alpha1.Subscription{}
	if err := r.Client.Get(ctx, req.NamespacedName, sub); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(sub, SubscriptionFinalizer) {
		patch := client.MergeFrom(sub.DeepCopy())
		controllerutil.AddFinalizer(sub, SubscriptionFinalizer)
		if err := r.Patch(ctx, sub, patch); err != nil {
			klog.Errorf("unable to register finalizer for subscription %s, error: %s", sub.Name, err)
			return ctrl.Result{}, err
		}
	}

	if ContainsAnnotation(sub, corev1alpha1.ForceDeleteAnnotation) &&
		(sub.Status.State == corev1alpha1.StateInstallFailed || sub.Status.State == corev1alpha1.StateUninstallFailed) {
		return r.forceDelete(ctx, sub)
	}

	if !sub.ObjectMeta.DeletionTimestamp.IsZero() {
		// enabled/disabled -> uninstalling -> uninstall failed/uninstalled
		return r.reconcileDelete(ctx, sub)
	}

	switch sub.Status.State {
	case "":
		// -> installing
		return r.installOrUpdate(ctx, sub)
	case corev1alpha1.StateInstalling:
		// installing -> installed or install failed
		return r.syncJobStatus(ctx, sub)
	case corev1alpha1.StateInstalled, corev1alpha1.StateDisabled, corev1alpha1.StateEnabled:
		// installed -> enabled/disabled
		// enabled <-> disabled
		return r.syncExtendedAPIStatus(ctx, sub)
	case corev1alpha1.StateInstallFailed, corev1alpha1.StateUninstallFailed:
		// The installation/uninstallation has failed, so do nothing
		break
	}

	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("subscription-controller").
		For(&corev1alpha1.Subscription{}).Complete(r)
}

func (r *SubscriptionReconciler) defaultHelmOptions() []helm.Option {
	options := make([]helm.Option, 0)
	// TODO support helm image option
	return options
}

// reconcileDelete delete the helm release involved and remove finalizer from subscription.
func (r *SubscriptionReconciler) reconcileDelete(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	options := r.defaultHelmOptions()
	helmExecutor, err := helm.NewExecutor(r.kubeconfig, sub.Status.TargetNamespace, sub.Status.ReleaseName, options...)
	if err != nil {
		return ctrl.Result{}, err
	}

	if sub.Status.JobName != "" {
		latestJobCondition, err := r.latestJobCondition(ctx, sub.Status.TargetNamespace, sub.Status.JobName)
		if err != nil {
			return ctrl.Result{}, err
		}
		sub = sub.DeepCopy()
		if latestJobCondition.Type == batchv1.JobComplete && latestJobCondition.Status == corev1.ConditionTrue {
			klog.V(4).Infof("remove the finalizer for subscription %s", sub.Name)
			return r.removeFinalizer(ctx, sub)
		} else if latestJobCondition.Type == batchv1.JobFailed && latestJobCondition.Status == corev1.ConditionTrue {
			updateStateAndCondition(sub, corev1alpha1.StateUninstallFailed, fmt.Sprintf("helm executor job failed: %s", latestJobCondition.Message))
			return r.updateSubscription(ctx, sub)
		}

		// Job is still running, check it later
		return ctrl.Result{
			RequeueAfter: time.Second * 3,
		}, nil
	}

	// It has not been installed correctly.
	if sub.Status.ReleaseName == "" {
		return r.removeFinalizer(ctx, sub)
	}

	if _, err = helmExecutor.Manifest(); err != nil {
		if !strings.Contains(err.Error(), "release: not found") {
			return ctrl.Result{}, err
		}
		// The involved release does not exist, just move on.
		return r.removeFinalizer(ctx, sub)
	}

	jobName, err := helmExecutor.Uninstall(ctx, helm.SetHelmJobLabels(map[string]string{
		corev1alpha1.SubscriptionReferenceLabel: sub.Name,
	}))
	if err != nil {
		klog.Errorf("delete helm release %s/%s failed, error: %s", sub.Status.TargetNamespace, sub.Status.ReleaseName, err)
		return ctrl.Result{}, err
	}

	klog.Infof("delete helm release %s/%s", sub.Status.TargetNamespace, sub.Status.ReleaseName)
	sub = sub.DeepCopy()
	sub.Status.JobName = jobName
	updateStateAndCondition(sub, corev1alpha1.StateUninstalling, "")
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) forceDelete(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	if sub.DeletionTimestamp == nil {
		// We need delete it first
		return ctrl.Result{}, r.Delete(ctx, sub)
	}

	options := r.defaultHelmOptions()
	helmExecutor, err := helm.NewExecutor(r.kubeconfig, sub.Status.TargetNamespace, sub.Status.ReleaseName, options...)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err = helmExecutor.ForceDelete(ctx); err != nil {
		return ctrl.Result{}, err
	}

	return r.removeFinalizer(ctx, sub)
}

func (r *SubscriptionReconciler) cleanupJobs(ctx context.Context, subName string) error {
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs, client.MatchingLabels{corev1alpha1.SubscriptionReferenceLabel: subName}); err != nil {
		return err
	}
	for i := range jobs.Items {
		if err := r.Delete(ctx, &jobs.Items[i]); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *SubscriptionReconciler) loadChartData(ctx context.Context, ref *corev1alpha1.ExtensionRef) ([]byte, error) {
	extensionVersion := &corev1alpha1.ExtensionVersion{}
	if err := r.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-%s", ref.Name, ref.Version)}, extensionVersion); err != nil {
		return nil, err
	}

	// load chart data from
	if extensionVersion.Spec.ChartDataRef != nil {
		configMap := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: extensionVersion.Spec.ChartDataRef.Namespace, Name: extensionVersion.Spec.ChartDataRef.Name}, configMap); err != nil {
			return nil, err
		}
		data := configMap.BinaryData[extensionVersion.Spec.ChartDataRef.Key]
		if data != nil {
			return data, nil
		}
		return nil, fmt.Errorf("binary data not found")
	}

	// load chart data from url
	if extensionVersion.Spec.ChartURL != "" {
		buf, err := r.helmGetter.Get(extensionVersion.Spec.ChartURL,
			getter.WithTimeout(5*time.Minute),
		)
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	return nil, fmt.Errorf("unable to load chart data")
}

func (r *SubscriptionReconciler) installOrUpdate(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	// Set Extension state to Preparing
	extension := &corev1alpha1.Extension{}
	if err := r.Get(ctx, types.NamespacedName{Name: sub.Spec.Extension.Name}, extension); err != nil {
		return ctrl.Result{}, err
	}
	if extension.Status.State != corev1alpha1.StatePreparing {
		extension.Status.State = corev1alpha1.StatePreparing
		if err := r.Update(ctx, extension); err != nil {
			return ctrl.Result{}, err
		}
	}

	// TODO: reconsider how to define the target namespace
	targetNamespace := fmt.Sprintf("extension-%s", sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name
	if err := r.initTargetNamespace(ctx, targetNamespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to init target namespace: %s", err)
	}
	options := r.defaultHelmOptions()
	options = append(options, helm.SetLabels(map[string]string{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}))
	helmExecutor, err := helm.NewExecutor(r.kubeconfig, targetNamespace, releaseName, options...)
	if err != nil {
		return ctrl.Result{}, err
	}

	charData, err := r.loadChartData(ctx, &sub.Spec.Extension)
	if err != nil {
		klog.Errorf("fail to load chart data for subscription: %s, error: %s", sub.Name, err)
		return ctrl.Result{}, err
	}

	jobLabels := map[string]string{
		corev1alpha1.SubscriptionReferenceLabel: sub.Name,
	}

	var jobName string
	if _, err = helmExecutor.Manifest(); err != nil {
		// release not exists or there is something wrong with the Manifest API
		if !strings.Contains(err.Error(), "release: not found") {
			return ctrl.Result{}, err
		}

		klog.Infof("install helm release %s/%s", targetNamespace, releaseName)
		jobName, err = helmExecutor.Install(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config), helm.SetHelmJobLabels(jobLabels))
		if err != nil {
			klog.Errorf("install helm release %s/%s failed, error: %s", targetNamespace, releaseName, err)
			return ctrl.Result{}, err
		}
		updateStateAndCondition(sub, corev1alpha1.StateInstalling, "")
	} else {
		// release exists, we need to upgrade it
		klog.Infof("upgrade helm release %s/%s", targetNamespace, releaseName)
		jobName, err = helmExecutor.Upgrade(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config), helm.SetHelmJobLabels(jobLabels))
		if err != nil {
			klog.Errorf("upgrade helm release %s/%s failed, error: %s", targetNamespace, releaseName, err)
			return ctrl.Result{}, err
		}
		updateStateAndCondition(sub, corev1alpha1.StateInstalling, "")
	}

	sub.Status.ReleaseName = releaseName
	sub.Status.TargetNamespace = targetNamespace
	sub.Status.JobName = jobName
	return r.updateSubscription(ctx, sub)
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

func (r *SubscriptionReconciler) syncJobStatus(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	if sub.Status.JobName == "" {
		// This is unlikely to happen in normal processes, and this is just to avoid subsequent exceptions
		return ctrl.Result{}, nil
	}
	latestJobCondition, err := r.latestJobCondition(ctx, sub.Status.TargetNamespace, sub.Status.JobName)
	if err != nil {
		return ctrl.Result{}, err
	}

	sub = sub.DeepCopy()
	if latestJobCondition.Type == batchv1.JobComplete && latestJobCondition.Status == corev1.ConditionTrue {
		// sync Subscription version to Extension's SubscribedVersion
		extension := &corev1alpha1.Extension{}
		if err = r.Get(ctx, types.NamespacedName{Name: sub.Spec.Extension.Name}, extension); err != nil {
			return ctrl.Result{}, err
		}
		if extension.Status.SubscribedVersion != sub.Spec.Extension.Version {
			extension.Status.SubscribedVersion = sub.Spec.Extension.Version
			if err = r.Update(ctx, extension); err != nil {
				return ctrl.Result{}, err
			}
		}

		sub.Status.JobName = ""
		updateStateAndCondition(sub, corev1alpha1.StateInstalled, "")
	} else if latestJobCondition.Type == batchv1.JobFailed && latestJobCondition.Status == corev1.ConditionTrue {
		updateStateAndCondition(sub, corev1alpha1.StateInstallFailed, fmt.Sprintf("helm executor job failed: %s", latestJobCondition.Message))
	} else {
		return ctrl.Result{RequeueAfter: time.Second * 3}, nil
	}

	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) latestJobCondition(ctx context.Context, namespace, name string) (batchv1.JobCondition, error) {
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, job); err != nil {
		return batchv1.JobCondition{}, err
	}
	jobConditions := job.Status.Conditions
	sort.Slice(jobConditions, func(i, j int) bool {
		return jobConditions[i].LastTransitionTime.After(jobConditions[j].LastTransitionTime.Time)
	})
	if len(job.Status.Conditions) > 0 {
		return jobConditions[0], nil
	}
	return batchv1.JobCondition{}, nil
}

func (r *SubscriptionReconciler) removeFinalizer(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	if err := r.cleanupJobs(ctx, sub.Name); err != nil {
		return ctrl.Result{}, err
	}
	// Remove the finalizer from the subscription and update it.
	controllerutil.RemoveFinalizer(sub, SubscriptionFinalizer)
	sub.Status.State = corev1alpha1.StateUninstalled
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) updateSubscription(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	if err := r.Update(ctx, sub); err != nil {
		return ctrl.Result{}, err
	}
	// sync extension state
	if err := r.syncExtensionState(ctx, sub); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) initTargetNamespace(ctx context.Context, namespace string) error {
	var ns corev1.Namespace
	err := r.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
	if errors.IsNotFound(err) {
		ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespace,
				Labels: map[string]string{tenantv1alpha1.WorkspaceLabel: SystemWorkspace},
			},
		}
		if err := r.Create(ctx, &ns); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// TODO support custom serviceaccount name
	subject := rbacv1.Subject{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      "default",
		Namespace: namespace,
	}

	var roleBinding rbacv1.RoleBinding
	err = r.Get(ctx, types.NamespacedName{Name: HelmExecutor, Namespace: namespace}, &roleBinding)
	if errors.IsNotFound(err) {
		roleBinding = rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      HelmExecutor,
				Namespace: namespace,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     "kubesphere:namespaced:helm-executor",
			},
			Subjects: []rbacv1.Subject{
				subject,
			},
		}
		if err := r.Create(ctx, &roleBinding); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	var clusterRoleBinding rbacv1.ClusterRoleBinding
	err = r.Get(ctx, types.NamespacedName{Name: HelmExecutor, Namespace: namespace}, &clusterRoleBinding)

	if errors.IsNotFound(err) {
		clusterRoleBinding = rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: HelmExecutor,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     "kubesphere:cluster:helm-executor",
			},
			Subjects: []rbacv1.Subject{
				subject,
			},
		}
		if err := r.Create(ctx, &clusterRoleBinding); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if !containsSubject(clusterRoleBinding.Subjects, subject) {
		update := clusterRoleBinding.DeepCopy()
		update.Subjects = append(update.Subjects, subject)
		if err := r.Update(ctx, update); err != nil {
			return err
		}
	}

	return nil
}

func containsSubject(subjects []rbacv1.Subject, subject rbacv1.Subject) bool {
	for _, item := range subjects {
		if subject.Kind == item.Kind && subject.Namespace == item.Namespace && subject.Name == item.Name {
			return true
		}
	}
	return false
}

func (r *SubscriptionReconciler) syncExtendedAPIStatus(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	jsbundles := &extensionsv1alpha1.JSBundleList{}
	if err := r.List(ctx, jsbundles, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return ctrl.Result{}, err
	}
	for _, item := range jsbundles.Items {
		if err := r.syncJSBundle(ctx, sub, item); err != nil {
			return ctrl.Result{}, err
		}
	}

	apiServices := &extensionsv1alpha1.APIServiceList{}
	if err := r.List(ctx, apiServices, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return ctrl.Result{}, err
	}
	for _, item := range apiServices.Items {
		if err := r.syncAPIService(ctx, sub, item); err != nil {
			return ctrl.Result{}, err
		}
	}

	reverseProxies := &extensionsv1alpha1.ReverseProxyList{}
	if err := r.List(ctx, reverseProxies, client.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: sub.Spec.Extension.Name}); err != nil {
		return ctrl.Result{}, err
	}
	for _, item := range reverseProxies.Items {
		if err := r.syncReverseProxy(ctx, sub, item); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.syncEnabledStatus(ctx, sub)
}

func (r *SubscriptionReconciler) syncJSBundle(ctx context.Context, sub *corev1alpha1.Subscription, jsbundle extensionsv1alpha1.JSBundle) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &extensionsv1alpha1.JSBundle{}
		err := r.Get(ctx, types.NamespacedName{Name: jsbundle.Name}, latest)
		if err != nil {
			return err
		}
		// TODO unavailable state should be considered
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateAvailable) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateAvailable
			} else {
				update.Status.State = extensionsv1alpha1.StateDisabled
			}
			if err := r.Update(ctx, update); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SubscriptionReconciler) syncAPIService(ctx context.Context, sub *corev1alpha1.Subscription, apiService extensionsv1alpha1.APIService) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &extensionsv1alpha1.APIService{}
		err := r.Get(ctx, types.NamespacedName{Name: apiService.Name}, latest)
		if err != nil {
			return err
		}
		// TODO unavailable state should be considered
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateAvailable) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateAvailable
			} else {
				update.Status.State = extensionsv1alpha1.StateDisabled
			}
			if err := r.Update(ctx, update); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SubscriptionReconciler) syncReverseProxy(ctx context.Context, sub *corev1alpha1.Subscription, reverseProxy extensionsv1alpha1.ReverseProxy) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &extensionsv1alpha1.ReverseProxy{}
		err := r.Get(ctx, types.NamespacedName{Name: reverseProxy.Name}, latest)
		if err != nil {
			return err
		}
		// TODO unavailable state should be considered
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateAvailable) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateAvailable
			} else {
				update.Status.State = extensionsv1alpha1.StateDisabled
			}
			if err := r.Update(ctx, update); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *SubscriptionReconciler) syncEnabledStatus(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	inconsistent := (sub.Spec.Enabled && sub.Status.State != extensionsv1alpha1.StateEnabled) ||
		(!sub.Spec.Enabled && sub.Status.State != extensionsv1alpha1.StateDisabled)
	if inconsistent {
		sub := sub.DeepCopy()
		if sub.Spec.Enabled {
			updateStateAndCondition(sub, corev1alpha1.StateEnabled, "")
		} else {
			updateStateAndCondition(sub, corev1alpha1.StateDisabled, "")
		}
		return r.updateSubscription(ctx, sub)
	}
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) syncExtensionState(ctx context.Context, sub *corev1alpha1.Subscription) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &corev1alpha1.Extension{}
		err := r.Get(ctx, types.NamespacedName{Name: sub.Spec.Extension.Name}, latest)
		if err != nil {
			return client.IgnoreNotFound(err)
		}
		if latest.Status.State != sub.Status.State {
			update := latest.DeepCopy()
			if sub.Status.State == corev1alpha1.StateUninstalled {
				update.Status.State = ""
				update.Status.SubscribedVersion = ""
			} else {
				update.Status.State = sub.Status.State
				update.Status.SubscribedVersion = sub.Spec.Extension.Version
			}
			if err := r.Update(ctx, update); err != nil {
				return err
			}
		}
		return nil
	})
}