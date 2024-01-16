/*
Copyright 2023 The KubeSphere Authors.

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
	"strings"
	"time"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	helmrepo "helm.sh/helm/v3/pkg/repo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/controller/application/installer"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

const helmRepoController = "helmrepo-controller"

var _ reconcile.Reconciler = &RepoReconciler{}
var _ kscontroller.Controller = &RepoReconciler{}

type RepoReconciler struct {
	recorder record.EventRecorder
	client.Client
}

func (r *RepoReconciler) Name() string {
	return helmRepoController
}

func (r *RepoReconciler) Enabled(clusterRole string) bool {
	return strings.EqualFold(clusterRole, string(clusterv1alpha1.ClusterRoleHost))
}

func (r *RepoReconciler) SetupWithManager(mgr *kscontroller.Manager) error {
	r.Client = mgr.GetClient()
	r.recorder = mgr.GetEventRecorderFor(helmRepoController)
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv2.Repo{}).
		Complete(r)
}

func (r *RepoReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {

	helmRepo := &appv2.Repo{}
	if err := r.Client.Get(ctx, request.NamespacedName, helmRepo); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if helmRepo.Spec.SyncPeriod == 0 {
		klog.Infof("no sync when SyncPeriod=0, repo: %s", helmRepo.GetName())
		helmRepo.Status.State = appv2.StatusNosync
		helmRepo.Status.LastUpdateTime = &metav1.Time{Time: time.Now()}
		err := r.Client.Status().Update(ctx, helmRepo)
		if err != nil {
			klog.Errorf("update status failed, error: %s", err)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	helmRepo.Status.State = appv2.StatusUpgrading
	helmRepo.Status.LastUpdateTime = &metav1.Time{Time: time.Now()}
	err := r.Client.Status().Update(ctx, helmRepo)
	if err != nil {
		klog.Errorf("update status failed, error: %s", err)
		return reconcile.Result{}, err
	}

	err = r.sync(ctx, helmRepo)
	if err != nil {
		return reconcile.Result{}, err
	}

	requeueAfter := time.Duration(helmRepo.Spec.SyncPeriod) * time.Second

	return reconcile.Result{RequeueAfter: requeueAfter}, nil
}

func (r *RepoReconciler) sync(ctx context.Context, helmRepo *appv2.Repo) (err error) {
	index, err := installer.LoadRepoIndex(helmRepo.Spec.Url, helmRepo.Spec.Credential)
	if err != nil {
		klog.Errorf("load index failed, repo: %s, url: %s, err: %s", helmRepo.GetName(), helmRepo.Spec.Url, err)
		return err
	}
	for name, versions := range index.Entries {
		if len(versions) == 0 {
			klog.Infof("no version found for %s", name)
			continue
		}
		if len(versions) > appv2.MaxNumOfVersions {
			versions = versions[:appv2.MaxNumOfVersions]
		}

		request := helmRepoAppRequest(helmRepo, versions, name)
		vRequests, err := helmRepoVersionRequest(r.Client, request, versions)
		if err != nil {
			return err
		}
		if err = application.CreateOrUpdateApp(ctx, r.Client, request, vRequests); err != nil {
			return err
		}
	}

	helmRepo.Status.State = appv2.StatusSuccessful
	helmRepo.Status.LastUpdateTime = &metav1.Time{Time: time.Now()}
	err = r.Client.Status().Update(ctx, helmRepo)
	if err != nil {
		klog.Errorf("update status failed, error: %s", err)
		return err
	}

	r.recorder.Eventf(helmRepo, corev1.EventTypeNormal, "Synced", "HelmRepo %s synced successfully", helmRepo.GetName())

	return err
}

func helmRepoAppRequest(repo *appv2.Repo, versions helmrepo.ChartVersions, name string) application.AppRequest {
	request := application.AppRequest{
		RepoName:    repo.Name,
		AppName:     fmt.Sprintf("%s-%s", repo.Name, name),
		Workspace:   repo.GetWorkspace(),
		Description: versions[0].Description,
		Icon:        versions[0].Icon,
		AppHome:     versions[0].Home,
		Url:         repo.Spec.Url,
		AppType:     appv2.AppTypeHelm,
		Credential:  repo.Spec.Credential,
	}

	return request
}

func helmRepoVersionRequest(cli client.Client, request application.AppRequest, versions helmrepo.ChartVersions) (result []application.AppRequest, err error) {
	appVersionList := &appv2.ApplicationVersionList{}

	opts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{appv2.RepoIDLabelKey: request.RepoName}),
	}
	err = cli.List(context.Background(), appVersionList, &opts)
	if err != nil {
		return nil, err
	}

	appVersionDigestMap := make(map[string]string)
	for _, i := range appVersionList.Items {
		//name format: repoName-appName-version
		appVersionDigestMap[i.Name] = i.Spec.Digest
	}

	for _, ver := range versions {
		key := fmt.Sprintf("%s-%s-%s", request.RepoName, ver.Name, ver.Version)
		dig := appVersionDigestMap[key]
		if dig == ver.Digest {
			continue
		}
		vRequest := application.AppRequest{
			RepoName:    request.RepoName,
			VersionName: ver.Version,
			AppName:     request.AppName,
			AppHome:     ver.Home,
			Icon:        ver.Icon,
			Digest:      ver.Digest,
			Description: ver.Description,
			AppType:     appv2.AppTypeHelm,
		}
		url := ver.URLs[0]
		if !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "s3://")) {
			url = request.Url + "/" + url
		}

		buf, err := installer.HelmPull(url, request.Credential)
		if err != nil {
			klog.Errorf("load chart failed, error: %s", err)
			continue
		}
		vRequest.Package = buf.Bytes()
		result = append(result, vRequest)
	}
	return result, nil
}
