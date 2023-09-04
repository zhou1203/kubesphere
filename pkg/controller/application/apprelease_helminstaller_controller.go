/*
Copyright 2023 KubeSphere Authors

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

package application

import (
	"context"
	"fmt"
	"os"

	"errors"

	batchv1 "k8s.io/api/batch/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	appv1alpha2 "kubesphere.io/api/application/v1alpha2"
	"kubesphere.io/utils/helm"

	helmrelease "helm.sh/helm/v3/pkg/release"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	helminstallerController  = "apprelease-helminstaller-controller"
	helminstallerName        = "kubesphere.io/helm-application"
	helminstallProtection    = "kubesphere.io/helminstall-protection"
	clusterRoleName          = "kubesphere:application:helminstaller"
	clusterRoleBindingFormat = "kubesphere:application:helminstaller-%s"
)

var _ reconcile.Reconciler = &AppReleaseHelmInstallerReconciler{}

type AppReleaseHelmInstallerReconciler struct {
	KubeConfigPath string
	kubeConfig     string
	client.Client
	Scheme           *runtime.Scheme
	clusterClientSet clusterclient.Interface
}

// Reconcile reads that state of the cluster for a helmreleases object and makes changes based on the state read
// and what is in the helmreleases.Spec
func (r *AppReleaseHelmInstallerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	apprls := &appv1alpha2.ApplicationRelease{}
	if err := r.Client.Get(ctx, req.NamespacedName, apprls); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(apprls, helminstallProtection) {
		apprls.ObjectMeta.Finalizers = append(apprls.ObjectMeta.Finalizers, helminstallProtection)
		return ctrl.Result{}, r.Update(ctx, apprls)
	}

	klog.V(4).Info("reconcile", "app release helminstaller", apprls.Name)

	if err := r.updateIngStatus(ctx, apprls); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.sync(ctx, apprls); err != nil {
		return ctrl.Result{}, err
	}

	klog.V(4).Info("synced", "app release  helminstaller", apprls.Name)

	return ctrl.Result{}, nil
}

func (r *AppReleaseHelmInstallerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	clusterClientSet, err := clusterclient.NewClusterClientSet(mgr.GetCache())
	if err != nil {
		return err
	}
	r.clusterClientSet = clusterClientSet

	if r.KubeConfigPath != "" {
		data, err := os.ReadFile(r.KubeConfigPath)
		if err != nil {
			return kscontroller.FailedToSetup(helminstallerController, fmt.Errorf("failed to load kubeconfig from file: %v", err))
		}
		r.kubeConfig = string(data)
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(helminstallerController).
		For(
			&appv1alpha2.ApplicationRelease{},
			builder.WithPredicates(
				predicate.NewPredicateFuncs(
					func(obj client.Object) bool {
						return r.isHelmApp(obj.(*appv1alpha2.ApplicationRelease))
					},
				),
				predicate.Funcs{
					GenericFunc: func(e event.GenericEvent) bool {
						return false
					},
				},
			),
		).
		Complete(r)
}

func (r *AppReleaseHelmInstallerReconciler) sync(ctx context.Context, apprls *appv1alpha2.ApplicationRelease) error {
	targetNamespace := apprls.GetRlsNamespace()
	releaseName := apprls.Name

	r.generateRoleBindingIfNotExists(ctx, targetNamespace)

	executor, err := helm.NewExecutor(r.kubeConfig, targetNamespace, releaseName,
		helm.SetJobLabels(labels.Set{appv1alpha2.AppReleaseReferenceLabelKey: apprls.Name}))
	if err != nil {
		klog.Error(err, "failed to create executor")
		return err
	}

	switch apprls.Status.State {
	case appv1alpha2.AppReleaseStatusCreating:
		return r.createOrUpgradeAppRelease(ctx, apprls, executor, false)
	case appv1alpha2.AppReleaseStatusUpgrading:
		return r.createOrUpgradeAppRelease(ctx, apprls, executor, true)
	case appv1alpha2.AppReleaseStatusDeleting:
		return r.uninstallAppRelease(ctx, apprls, executor)
	case appv1alpha2.AppReleaseStatusFailed:
		return nil
	case appv1alpha2.AppReleaseStatusCreated, appv1alpha2.AppReleaseStatusUpgraded:
		return r.checkHelmReleaseAndUpdateAppReleaseStatus(ctx, apprls, executor)
	}

	return nil
}

func (r *AppReleaseHelmInstallerReconciler) checkHelmReleaseAndUpdateAppReleaseStatus(
	ctx context.Context, apprls *appv1alpha2.ApplicationRelease, executor helm.Executor) error {
	// Check if the target helm release exists, and check the status.
	// If don't find the target helm release, retry until do.
	release, err := executor.Release()
	if err != nil {
		// Job failed to execute during startup due to an environmental, such as image not being fetched
		// update app release failed status based on job state
		if apprls.Status.JobName != "" {
			jobKey := client.ObjectKey{Namespace: apprls.GetRlsNamespace(), Name: apprls.Status.JobName}
			if r.isJobStatusFailed(ctx, jobKey) {
				return r.updateStatus(ctx, apprls, appv1alpha2.AppReleaseStatusFailed)
			}
		}

		return fmt.Errorf("%v, retrying", err)
	}

	switch release.Info.Status {
	case helmrelease.StatusFailed:
		return r.updateStatus(ctx, apprls, appv1alpha2.AppReleaseStatusFailed)
	case helmrelease.StatusDeployed:
		return r.updateStatus(ctx, apprls, appv1alpha2.AppReleaseStatusActive)
	}

	return nil
}

func (r *AppReleaseHelmInstallerReconciler) isJobStatusFailed(ctx context.Context, jobKey client.ObjectKey) bool {
	job := &batchv1.Job{}
	if err := r.Get(ctx, jobKey, job); err != nil {
		// return false, allow retry
		klog.Errorf("fetch job failed, jobKey:%s, err:%v", jobKey.String(), err)
		return false
	}

	if job.Status.Failed > 0 || (job.Spec.BackoffLimit != nil && job.Status.Failed > *job.Spec.BackoffLimit) {
		return true
	}

	return false
}

func (r *AppReleaseHelmInstallerReconciler) loadData(ctx context.Context, appVerionId string) ([]byte, error) {
	appVersion := &appv1alpha2.ApplicationVersion{}
	if err := r.Get(ctx, client.ObjectKey{Name: appVerionId}, appVersion); err != nil {
		return nil, err
	}

	configMap := &corev1.ConfigMap{}
	cmKey := client.ObjectKey{Namespace: appVersion.Spec.DataRef.Namespace, Name: appVersion.Spec.DataRef.Name}
	if err := r.Get(ctx, cmKey, configMap); err != nil {
		return nil, err
	}

	if data := configMap.BinaryData[appVersion.Spec.DataRef.Key]; data != nil {
		return data, nil
	}

	return nil, fmt.Errorf("binary data not found")
}

func (r *AppReleaseHelmInstallerReconciler) uninstallAppRelease(ctx context.Context, rls *appv1alpha2.ApplicationRelease, executor helm.Executor) error {
	if err := executor.ForceDelete(ctx); err != nil {
		klog.Error(err, "failed to force delete helm release")
		return err
	}

	deletePolicy := metav1.DeletePropagationBackground
	if err := r.DeleteAllOf(ctx, &batchv1.Job{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{
			Namespace:     rls.GetRlsNamespace(),
			LabelSelector: labels.SelectorFromSet(labels.Set{appv1alpha2.AppReleaseReferenceLabelKey: rls.Name}),
		},
		DeleteOptions: client.DeleteOptions{PropagationPolicy: &deletePolicy},
	}); err != nil {
		return fmt.Errorf("failed to delete related helm executor jobs: %s", err)
	}

	// Remove the finalizer from the app relase and update it.
	controllerutil.RemoveFinalizer(rls, helminstallProtection)
	return r.Update(ctx, rls)
}
func (r *AppReleaseHelmInstallerReconciler) createOrUpgradeAppRelease(ctx context.Context, rls *appv1alpha2.ApplicationRelease, executor helm.Executor, upgrade bool) error {
	data, err := r.loadData(ctx, rls.Spec.AppVersionID)
	if err != nil {
		return err
	}

	cluster, err := r.clusterClientSet.Get(rls.GetRlsCluster())
	if err != nil {
		return err
	}

	options := []helm.HelmOption{
		// if a release by this name doesn't already exist, run an install
		helm.SetInstall(true),
		helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)),
		helm.SetKubeAsUser(rls.Annotations[appv1alpha2.ReqUserAnnotationKey]),
		helm.SetLabels(labels.Set{helminstallProtection: rls.Name}),
	}

	if rls.Status.JobName, err = executor.Upgrade(ctx, rls.Name, data, rls.Spec.Values, options...); err != nil {
		klog.Errorf("failed to create executor job, err: %v", err)
		return r.updateStatus(ctx, rls, appv1alpha2.AppReleaseStatusFailed, "failed to create executor job")
	}

	if upgrade {
		return r.updateStatus(ctx, rls, appv1alpha2.AppReleaseStatusUpgraded)
	}

	return r.updateStatus(ctx, rls, appv1alpha2.AppReleaseStatusCreated)
}

func (r *AppReleaseHelmInstallerReconciler) updateStatus(ctx context.Context, apprls *appv1alpha2.ApplicationRelease, status string, message ...string) error {
	apprls.Status.State = status
	apprls.Status.LastUpdate = metav1.Now()

	newState := appv1alpha2.ApplicationReleaseDeployStatus{
		State: status,
		Time:  metav1.Now(),
	}
	if len(message) > 0 {
		newState.Message = message[0]
	}

	if len(apprls.Status.DeployStatus) >= appv1alpha2.MaxStateNum {
		apprls.Status.DeployStatus = (apprls.Status.DeployStatus)[1:]
	}
	apprls.Status.DeployStatus = append(apprls.Status.DeployStatus, newState)

	return r.Status().Update(ctx, apprls)
}

func (r *AppReleaseHelmInstallerReconciler) updateIngStatus(ctx context.Context, apprls *appv1alpha2.ApplicationRelease) error {
	if apprls.Status.State == "" {
		apprls.Status.State = appv1alpha2.AppReleaseStatusCreating
		apprls.Status.SpecHash = apprls.HashSpec()

	} else if !apprls.ObjectMeta.DeletionTimestamp.IsZero() &&
		apprls.Status.State != appv1alpha2.AppReleaseStatusDeleting {
		apprls.Status.State = appv1alpha2.AppReleaseStatusDeleting

	} else if apprls.Status.State != appv1alpha2.AppReleaseStatusUpgrading &&
		apprls.HashSpec() != apprls.Status.SpecHash {
		apprls.Status.State = appv1alpha2.AppReleaseStatusUpgrading
		apprls.Status.SpecHash = apprls.HashSpec()

	} else {
		// Avoid repeatedly triggering sync.
		switch apprls.Status.State {
		case appv1alpha2.AppReleaseStatusCreating:
			return errors.New(appv1alpha2.AppReleaseStatusCreating)
		case appv1alpha2.AppReleaseStatusUpgrading:
			return errors.New(appv1alpha2.AppReleaseStatusUpgrading)
		case appv1alpha2.AppReleaseStatusDeleting:
			return errors.New(appv1alpha2.AppReleaseStatusDeleting)
		default:
			return nil
		}
	}

	apprls.Status.LastUpdate = metav1.Now()

	return r.Status().Update(ctx, apprls)
}

func (r *AppReleaseHelmInstallerReconciler) isHelmApp(rls *appv1alpha2.ApplicationRelease) bool {
	appVersion := &appv1alpha2.ApplicationVersion{}
	appVersionKey := client.ObjectKey{Name: rls.Spec.AppVersionID}
	if err := r.Get(context.TODO(), appVersionKey, appVersion); err != nil {
		klog.Errorf("failed fetch ApplicationVersion of name: %s", rls.Spec.AppVersionID)
	}

	appClass := &appv1alpha2.ApplicationClass{}
	appClassKey := client.ObjectKey{Name: appVersion.Spec.AppClassName}
	if err := r.Get(context.TODO(), appClassKey, appClass); err != nil {
		klog.Errorf("failed fetch ApplicationClass of name: %s", appVersion.Spec.AppClassName)
	}

	if appClass.Spec.Installer == helminstallerName {
		return true
	}

	return false
}

func (r *AppReleaseHelmInstallerReconciler) generateRoleBindingIfNotExists(ctx context.Context, namespace string) error {
	clusterRoleBinding := rbacv1.ClusterRoleBinding{}
	roleBindingName := fmt.Sprintf(clusterRoleBindingFormat, namespace)

	if err := r.Get(ctx, client.ObjectKey{Name: roleBindingName}, &clusterRoleBinding); err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRoleBinding = rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: roleBindingName},
				RoleRef: rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "ClusterRole",
					Name:     clusterRoleName,
				},
				Subjects: []rbacv1.Subject{{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      "default",
					Namespace: namespace,
				}},
			}
			ns := &corev1.Namespace{}
			if err := r.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
				return err
			}

			if err := controllerutil.SetControllerReference(ns, &clusterRoleBinding, r.Scheme); err != nil {
				klog.Error(err, "failed to set owner reference")
				return err
			}

			if err := r.Create(ctx, &clusterRoleBinding, &client.CreateOptions{}); err != nil {
				klog.Error(err, "failed to create app release clusterRoleBinding")
				return err
			}
			klog.V(4).Info("app release clusterRoleBinding create successfully")
		}
	}

	return nil
}
