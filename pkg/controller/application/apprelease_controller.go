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

	helmrelease "helm.sh/helm/v3/pkg/release"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	appv2 "kubesphere.io/api/application/v2"
	"kubesphere.io/utils/helm"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/controller/application/installer"
	"kubesphere.io/kubesphere/pkg/simple/client/application"

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
	clusterClientSet clusterclient.Interface
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

	// dispatch status
	if apprls.Status.State == "" {
		apprls.Status.SpecHash = apprls.HashSpec()
		return ctrl.Result{}, r.updateStatus(ctx, apprls, appv2.StatusCreating)

	} else if apprls.HashSpec() != apprls.Status.SpecHash {
		apprls.Status.SpecHash = apprls.HashSpec()
		return ctrl.Result{}, r.updateStatus(ctx, apprls, appv2.StatusUpgrading)
	}

	switch apprls.Status.State {
	case appv2.StatusCreating, appv2.StatusUpgrading:
		return ctrl.Result{}, r.createOrUpgradeAppRelease(ctx, apprls, executor)

	case appv2.StatusCreated, appv2.StatusUpgraded:
		return ctrl.Result{}, r.checkHelmReleaseAndUpdateAppReleaseStatus(ctx, apprls, executor)

	case appv2.StatusFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AppReleaseReconciler) checkHelmReleaseAndUpdateAppReleaseStatus(
	ctx context.Context, apprls *appv2.ApplicationRelease, executor helm.Executor) error {
	// Check if the target helm release exists, and check the status.
	// If don't find the target helm release, retry until do.
	release, err := executor.Release()
	if err != nil {
		// Job failed to execute during startup due to an environmental, such as image not being fetched
		// update app release failed status based on job state
		if apprls.Status.JobName != "" {
			jobKey := client.ObjectKey{Namespace: apprls.GetRlsNamespace(), Name: apprls.Status.JobName}
			if r.isJobStatusFailed(ctx, jobKey) {
				return r.updateStatus(ctx, apprls, appv2.StatusFailed, fmt.Sprintf("job %s failed %s", apprls.Status.JobName, err.Error()))
			}
		}

		return fmt.Errorf("%v, retrying", err)
	}

	// TODO if failed, add job logs to message
	switch release.Info.Status {
	case helmrelease.StatusFailed:
		return r.updateStatus(ctx, apprls, appv2.StatusFailed, release.Info.Description)
	case helmrelease.StatusDeployed:
		return r.updateStatus(ctx, apprls, appv2.StatusActive)
	}

	return nil
}

func (r *AppReleaseReconciler) getExecutor(apprls *appv2.ApplicationRelease) (executor helm.Executor, err error) {
	runClient, dynamicClient, configByte, err := r.getClusterInfo(apprls.GetRlsCluster())
	if err != nil {
		return nil, err
	}
	if apprls.Spec.AppType == appv2.AppTypeYaml || apprls.Spec.AppType == appv2.AppTypeEdge {

		jsonList, err := application.ReadYaml(apprls.Spec.Values)
		if err != nil {
			return nil, err
		}
		var gvrListInfo []installer.InsInfo
		for _, i := range jsonList {
			gvr, utd, err := application.GetInfoFromBytes(i, runClient.RESTMapper())
			if err != nil {
				return nil, err
			}
			ins := installer.InsInfo{
				GroupVersionResource: gvr,
				Name:                 utd.GetName(),
				Namespace:            utd.GetNamespace(),
			}
			gvrListInfo = append(gvrListInfo, ins)
		}

		executor = installer.YamlInstaller{
			Mapper:      runClient.RESTMapper(),
			DynamicCli:  dynamicClient,
			GvrListInfo: gvrListInfo,
			Namespace:   apprls.GetRlsNamespace(),
		}
		return executor, nil
	}

	opt1 := helm.SetHelmImage(r.HelmImage)
	opt2 := helm.SetJobLabels(labels.Set{appv2.AppReleaseReferenceLabelKey: apprls.Name})
	executor, err = helm.NewExecutor(string(configByte), apprls.GetRlsNamespace(), apprls.Name, opt1, opt2)
	if err != nil {
		return nil, err
	}
	// helm chart install need the clusterrolebinding
	if err = r.generateRoleBinding(context.TODO(), runClient, apprls.GetRlsNamespace()); err != nil {
		klog.Errorf("generate clusterrolebinding fail %s", err)
		return nil, err
	}

	return executor, err
}

func (r *AppReleaseReconciler) getClusterInfo(clusterName string) (client.Client, *dynamic.DynamicClient, []byte, error) {
	c, err := r.clusterClientSet.Get(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}

	runtimeClient, err := r.clusterClientSet.GetRuntimeClient(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}
	clusterClient, err := r.clusterClientSet.GetClusterClient(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(clusterClient.RestConfig)
	if err != nil {
		return nil, nil, nil, err
	}
	return runtimeClient, dynamicClient, c.Spec.Connection.KubeConfig, nil
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

	return nil

}
func (r *AppReleaseReconciler) createOrUpgradeAppRelease(ctx context.Context, rls *appv2.ApplicationRelease, executor helm.Executor) error {
	data, err := r.loadData(ctx, rls.Spec.AppVersionID)
	if err != nil {
		return err
	}
	clusterName := rls.GetRlsCluster()
	c, err := r.clusterClientSet.Get(clusterName)
	if err != nil {
		return err
	}

	options := []helm.HelmOption{
		helm.SetInstall(true),
		helm.SetHelmKubeConfig(string(c.Spec.Connection.KubeConfig)),
		helm.SetKubeAsUser(rls.Annotations[appv2.ReqUserAnnotationKey]),
	}

	if rls.Status.JobName, err = executor.Upgrade(ctx, rls.Name, data, rls.Spec.Values, options...); err != nil {
		klog.Errorf("failed to create executor job, err: %v", err)
		return r.updateStatus(ctx, rls, appv2.StatusFailed, err.Error())
	}

	return r.updateStatus(ctx, rls, appv2.StatusCreated)
}

func (r *AppReleaseReconciler) updateStatus(ctx context.Context, apprls *appv2.ApplicationRelease, status string, message ...string) error {
	apprls.Status.State = status
	if message != nil {
		apprls.Status.Message = message[0]
	}
	apprls.Status.LastUpdate = metav1.Now()
	return r.Status().Update(ctx, apprls)
}

func (r *AppReleaseReconciler) generateRoleBinding(ctx context.Context, client client.Client, namespace string) error {
	var ns corev1.Namespace
	err := client.Get(ctx, types.NamespacedName{Name: namespace}, &ns)
	if err != nil {
		return err
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	clusterRoleBinding.Name = fmt.Sprintf(clusterRoleBindingFormat, namespace)
	_, err = controllerutil.CreateOrUpdate(ctx, client, clusterRoleBinding, func() error {
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
