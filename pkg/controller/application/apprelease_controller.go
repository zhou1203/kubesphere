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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	appv2 "kubesphere.io/api/application/v2"
	"kubesphere.io/utils/helm"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/controller/application/installer"

	helmrelease "helm.sh/helm/v3/pkg/release"

	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	helminstallerController  = "apprelease-helminstaller-controller"
	clusterRoleName          = "kubesphere:application:helminstaller"
	clusterRoleBindingFormat = "kubesphere:application:helminstaller-%s"
	HelmReleaseFinalizer     = "helmrelease.application.kubesphere.io"
)

var _ reconcile.Reconciler = &AppReleaseReconciler{}

func (r *AppReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	clusterClientSet, err := clusterclient.NewClusterClientSet(mgr.GetCache())
	if err != nil {
		return err
	}
	r.clusterClientSet = clusterClientSet

	return ctrl.NewControllerManagedBy(mgr).Named(helminstallerController).
		For(&appv2.ApplicationRelease{}).Complete(r)
}

type AppReleaseReconciler struct {
	client.Client
	RestConfig       *rest.Config
	clusterClientSet clusterclient.Interface
	KubeConfigPath   string
	HelmImage        string
}

func (r *AppReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	apprls := &appv2.ApplicationRelease{}
	if err := r.Client.Get(ctx, req.NamespacedName, apprls); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	executor, err := r.getExecutor(apprls)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !controllerutil.ContainsFinalizer(apprls, HelmReleaseFinalizer) {
		controllerutil.AddFinalizer(apprls, HelmReleaseFinalizer)
		err := r.Update(ctx, apprls)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if !apprls.ObjectMeta.DeletionTimestamp.IsZero() {
		err = r.updateStatus(ctx, apprls, appv2.StatusDeleting)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.uninstall(ctx, apprls, executor)
		if err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(apprls, HelmReleaseFinalizer)
		err = r.Update(ctx, apprls)
		return ctrl.Result{}, err
	}

	if apprls.HashSpec() != apprls.Status.SpecHash {
		apprls.Status.SpecHash = apprls.HashSpec()
		err := r.updateStatus(ctx, apprls, appv2.StatusUpgrading)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.generateRoleBinding(ctx, apprls.GetRlsNamespace()); err != nil {
		return ctrl.Result{}, err
	}

	switch apprls.Status.State {
	case "":
		apprls.Status.SpecHash = apprls.HashSpec()
		err = r.updateStatus(ctx, apprls, appv2.StatusCreating)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	case appv2.StatusCreating:
		err = r.createOrUpgradeAppRelease(ctx, apprls, executor)
		if err != nil {
			return ctrl.Result{}, err
		}

	case appv2.StatusUpgrading:
		err = r.createOrUpgradeAppRelease(ctx, apprls, executor)
		if err != nil {
			return ctrl.Result{}, err
		}
	case appv2.StatusCreated:
		release, err := executor.Release()
		if err != nil && apprls.Status.JobName != "" {
			jobKey := client.ObjectKey{Namespace: apprls.GetRlsNamespace(), Name: apprls.Status.JobName}
			if r.isJobStatusFailed(ctx, jobKey) {
				if err = r.updateStatus(ctx, apprls, appv2.StatusFailed); err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		switch release.Info.Status {
		case helmrelease.StatusFailed:
			err = r.updateStatus(ctx, apprls, appv2.StatusFailed)
			if err != nil {
				return ctrl.Result{}, err
			}
		case helmrelease.StatusDeployed:
			err = r.updateStatus(ctx, apprls, appv2.StatusActive)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *AppReleaseReconciler) getExecutor(apprls *appv2.ApplicationRelease) (executor helm.Executor, err error) {

	if apprls.Spec.AppType == appv2.AppTypeYaml {

		dc, err := dynamic.NewForConfig(r.RestConfig)
		if err != nil {
			klog.Errorf("failed to create dynamic client, err: %v", err)
			return executor, err
		}
		executor = installer.YamlInstaller{
			Mapper:     r.Client.RESTMapper(),
			DynamicCli: dc,
			AppRlsReference: metav1.OwnerReference{
				APIVersion: apprls.APIVersion,
				Kind:       apprls.Kind,
				Name:       apprls.Name,
				UID:        apprls.UID,
			},
		}
		return executor, nil
	}

	opt1 := helm.SetHelmImage(r.HelmImage)
	opt2 := helm.SetJobLabels(labels.Set{appv2.AppReleaseReferenceLabelKey: apprls.Name})
	data, err := os.ReadFile(r.KubeConfigPath)
	if err != nil {
		klog.Errorf("failed to read kubeconfig file, err: %v", err)
		return executor, err
	}
	executor, err = helm.NewExecutor(string(data), apprls.GetRlsNamespace(), apprls.Name, opt1, opt2)

	return executor, err
}

func (r *AppReleaseReconciler) isJobStatusFailed(ctx context.Context, jobKey client.ObjectKey) bool {
	job := &batchv1.Job{}
	if err := r.Get(ctx, jobKey, job); err != nil {
		klog.Errorf("fetch job failed, jobKey:%s, err:%v", jobKey.String(), err)
		return false
	}

	if job.Status.Failed > 0 || job.Status.Failed > *job.Spec.BackoffLimit {
		return true
	}

	return false
}

func (r *AppReleaseReconciler) loadData(ctx context.Context, appVersionId string) ([]byte, error) {
	configMap := &corev1.ConfigMap{}
	cmKey := client.ObjectKey{Namespace: constants.KubeSphereNamespace, Name: appVersionId}
	err := r.Get(ctx, cmKey, configMap)
	return configMap.BinaryData[appv2.BinaryKey], err
}

func (r *AppReleaseReconciler) uninstall(ctx context.Context, rls *appv2.ApplicationRelease, executor helm.Executor) error {
	if err := executor.ForceDelete(ctx); err != nil {
		klog.Error(err, "failed to force delete helm release")
		return err
	}

	deletePolicy := metav1.DeletePropagationBackground
	err := r.DeleteAllOf(ctx, &batchv1.Job{}, &client.DeleteAllOfOptions{
		ListOptions: client.ListOptions{
			Namespace:     rls.GetRlsNamespace(),
			LabelSelector: labels.SelectorFromSet(labels.Set{appv2.AppReleaseReferenceLabelKey: rls.Name}),
		},
		DeleteOptions: client.DeleteOptions{PropagationPolicy: &deletePolicy},
	})
	return err

}
func (r *AppReleaseReconciler) createOrUpgradeAppRelease(ctx context.Context, rls *appv2.ApplicationRelease, executor helm.Executor) error {
	data, err := r.loadData(ctx, rls.Spec.AppVersionID)
	if err != nil {
		return err
	}

	clusterName := rls.GetRlsCluster()
	if clusterName == "" {
		//todo
		clusterName = "host"
	}
	cluster, err := r.clusterClientSet.Get(clusterName)
	if err != nil {
		return err
	}

	options := []helm.HelmOption{
		helm.SetInstall(true),
		helm.SetHelmKubeConfig(string(cluster.Spec.Connection.KubeConfig)),
		helm.SetKubeAsUser(rls.Annotations[appv2.ReqUserAnnotationKey]),
	}

	if rls.Status.JobName, err = executor.Upgrade(ctx, rls.Name, data, rls.Spec.Values, options...); err != nil {
		klog.Errorf("failed to create executor job, err: %v", err)
		return r.updateStatus(ctx, rls, appv2.StatusFailed)
	}

	return r.updateStatus(ctx, rls, appv2.StatusCreated)
}

func (r *AppReleaseReconciler) updateStatus(ctx context.Context, apprls *appv2.ApplicationRelease, status string) error {
	apprls.Status.State = status
	apprls.Status.LastUpdate = metav1.Now()
	return r.Status().Update(ctx, apprls)
}

func (r *AppReleaseReconciler) generateRoleBinding(ctx context.Context, namespace string) error {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		return err
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	clusterRoleBinding.Name = fmt.Sprintf(clusterRoleBindingFormat, namespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, clusterRoleBinding, func() error {
		clusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		}
		clusterRoleBinding.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      "default",
			Namespace: namespace,
		}}
		owns := []metav1.OwnerReference{{
			APIVersion:         ns.APIVersion,
			Kind:               ns.Kind,
			Name:               ns.Name,
			UID:                ns.UID,
			BlockOwnerDeletion: pointer.Bool(true),
			Controller:         pointer.Bool(true),
		}}
		clusterRoleBinding.SetOwnerReferences(owns)
		return nil
	})

	return err
}
