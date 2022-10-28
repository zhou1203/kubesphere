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
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/getter"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
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

	if sub.ObjectMeta.DeletionTimestamp != nil {
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
		jobCondition, err := r.jobCondition(ctx, sub.Status.TargetNamespace, sub.Status.JobName)
		if err != nil {
			return ctrl.Result{}, err
		}
		sub = sub.DeepCopy()
		if jobCondition == batchv1.JobComplete {
			klog.V(4).Infof("remove the finalizer for subscription %s", sub.Name)
			sub.Status.State = corev1alpha1.StateUninstalled
			return r.removeFinalizer(ctx, sub)
		}
		if jobCondition == batchv1.JobFailed {
			sub.Status.State = corev1alpha1.StateUninstallFailed
			return r.updateSubscription(ctx, sub)
		}
		// Job is still running, check it later
		return ctrl.Result{
			RequeueAfter: time.Second * 3,
		}, nil
	}

	if _, err = helmExecutor.Manifest(); err != nil {
		if !strings.Contains(err.Error(), "release: not found") {
			return ctrl.Result{}, err
		}
		// The involved release does not exist, just move on.
		return r.removeFinalizer(ctx, sub)
	}

	jobName, err := helmExecutor.Uninstall(ctx)
	if err != nil {
		klog.Errorf("delete helm release %s/%s failed, error: %s", sub.Status.TargetNamespace, sub.Status.ReleaseName, err)
		return ctrl.Result{}, err
	}

	klog.Infof("delete helm release %s/%s", sub.Status.TargetNamespace, sub.Status.ReleaseName)
	sub.Status.JobName = jobName
	sub.Status.State = corev1alpha1.StateUninstalling
	return r.updateSubscription(ctx, sub)
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

	var jobName string
	if _, err = helmExecutor.Manifest(); err != nil {
		// release not exists or there is something wrong with the Manifest API
		if !strings.Contains(err.Error(), "release: not found") {
			return ctrl.Result{}, err
		}

		klog.Infof("install helm release %s/%s", targetNamespace, releaseName)
		jobName, err = helmExecutor.Install(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config))
		if err != nil {
			klog.Errorf("install helm release %s/%s failed, error: %s", targetNamespace, releaseName, err)
			return ctrl.Result{}, err
		}
	} else {
		// release exists, we need to upgrade it
		klog.Infof("upgrade helm release %s/%s", targetNamespace, releaseName)
		jobName, err = helmExecutor.Upgrade(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config))
		if err != nil {
			klog.Errorf("upgrade helm release %s/%s failed, error: %s", targetNamespace, releaseName, err)
			return ctrl.Result{}, err
		}
	}

	sub = sub.DeepCopy()
	// TODO: Add more conditions
	sub.Status = corev1alpha1.SubscriptionStatus{
		State:           corev1alpha1.StateInstalling,
		ReleaseName:     releaseName,
		TargetNamespace: targetNamespace,
		JobName:         jobName,
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) syncJobStatus(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	if sub.Status.JobName == "" {
		// This is unlikely to happen in normal processes, and this is just to avoid subsequent exceptions
		return ctrl.Result{}, nil
	}
	jobCondition, err := r.jobCondition(ctx, sub.Status.TargetNamespace, sub.Status.JobName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if jobCondition == "" {
		// Job is still running, check it later
		return ctrl.Result{
			RequeueAfter: time.Second * 3,
		}, nil
	}

	sub = sub.DeepCopy()
	if jobCondition == batchv1.JobComplete {
		sub.Status.JobName = ""
		sub.Status.State = corev1alpha1.StateInstalled
	}
	if jobCondition == batchv1.JobFailed {
		sub.Status.State = corev1alpha1.StateInstallFailed
	}
	return r.updateSubscription(ctx, sub)
}

func (r *SubscriptionReconciler) jobCondition(ctx context.Context, namespace, name string) (batchv1.JobConditionType, error) {
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, job); err != nil {
		return "", err
	}
	if job.Status.Succeeded > 0 {
		return batchv1.JobComplete, nil
	}
	if job.Status.Failed > 0 {
		return batchv1.JobFailed, nil
	}
	return "", nil
}

func (r *SubscriptionReconciler) removeFinalizer(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	// Remove the finalizer from the subscription and update it.
	controllerutil.RemoveFinalizer(sub, SubscriptionFinalizer)
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
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
		if errors.IsNotFound(err) {
			ns.ObjectMeta = metav1.ObjectMeta{
				Name:   namespace,
				Labels: map[string]string{tenantv1alpha1.WorkspaceLabel: SystemWorkspace},
			}
			if err := r.Create(ctx, ns); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// TODO support custom serviceaccount name
	roleBinding := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: HelmExecutor, Namespace: namespace}, roleBinding); err != nil {
		if errors.IsNotFound(err) {
			roleBinding.ObjectMeta = metav1.ObjectMeta{
				Name:      HelmExecutor,
				Namespace: namespace,
			}
			roleBinding.RoleRef = rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     HelmExecutor,
			}
			roleBinding.Subjects = []rbacv1.Subject{
				{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      "default",
					Namespace: namespace,
				},
			}
			if err := r.Create(ctx, roleBinding); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil
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
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateEnabled) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateEnabled
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
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateEnabled) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateEnabled
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
		inconsistent := (sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateEnabled) ||
			(!sub.Spec.Enabled && latest.Status.State != extensionsv1alpha1.StateDisabled)

		if inconsistent {
			update := latest.DeepCopy()
			if sub.Spec.Enabled {
				update.Status.State = extensionsv1alpha1.StateEnabled
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
			sub.Status.State = corev1alpha1.StateEnabled
		} else {
			sub.Status.State = corev1alpha1.StateDisabled
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
			} else {
				update.Status.State = sub.Status.State
			}
			if err := r.Update(ctx, update); err != nil {
				return err
			}
		}
		return nil
	})
}
