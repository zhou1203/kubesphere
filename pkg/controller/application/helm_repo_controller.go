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
	"math"
	"strings"
	"time"

	helmrepo "helm.sh/helm/v3/pkg/repo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"kubesphere.io/api/application/v1alpha2"
	appv1alpha2 "kubesphere.io/api/application/v1alpha2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/simple/client/helmrepoindex"
	"kubesphere.io/kubesphere/pkg/utils/idutils"
	"kubesphere.io/kubesphere/pkg/utils/stringutils"
)

const (
	MinRetryDuration      = 60
	MaxRetryDuration      = 600
	HelmRepoSyncStateLen  = 10
	HelmRepoMinSyncPeriod = 180
	MessageLen            = 512

	helmRepoFinalizer  = "helmrepo.application.kubesphere.io"
	helmRepoController = "helm-repo-controller"

	defaultAppClassName = "ks-helm"
	defaultNamespace    = "kubesphere-system"
)

var _ reconcile.Reconciler = &HelmRepoReconciler{}

type HelmRepoReconciler struct {
	client.Client
}

func (r *HelmRepoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		Named(helmRepoController).
		For(&appv1alpha2.HelmRepo{}).
		Complete(r)
}

func (r *HelmRepoReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	start := time.Now()
	klog.Infof("sync repo: %s", request.Name)
	defer func() {
		klog.Infof("sync repo end: %s, elapsed: %v", request.Name, time.Since(start))
	}()

	helmRepo := &appv1alpha2.HelmRepo{}
	if err := r.Client.Get(ctx, request.NamespacedName, helmRepo); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// manage finalizer
	if !controllerutil.ContainsFinalizer(helmRepo, helmRepoFinalizer) {
		helmRepo.ObjectMeta.Finalizers = append(helmRepo.ObjectMeta.Finalizers, helmRepoFinalizer)
		return ctrl.Result{}, r.Update(ctx, helmRepo)
	}
	if !helmRepo.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(helmRepo, helmRepoFinalizer)
		return reconcile.Result{}, r.Update(ctx, helmRepo)
	}

	if helmRepo.Spec.SyncPeriod != 0 {
		helmRepo.Spec.SyncPeriod = int(math.Max(float64(helmRepo.Spec.SyncPeriod), HelmRepoMinSyncPeriod))
	}

	retryAfter := 0
	if syncNow, after := needReSyncNow(helmRepo); syncNow {
		// sync repo
		syncErr := r.syncRepo(ctx, helmRepo)
		state := helmRepo.Status.SyncState
		now := metav1.Now()

		if syncErr != nil {
			state = append([]appv1alpha2.HelmRepoSyncState{{
				State:    appv1alpha2.RepoStateFailed,
				Message:  stringutils.ShortenString(syncErr.Error(), MessageLen),
				SyncTime: &now,
			}}, state...)
			helmRepo.Status.State = appv1alpha2.RepoStateFailed

		} else {
			state = append([]appv1alpha2.HelmRepoSyncState{{
				State:    appv1alpha2.RepoStateSuccessful,
				SyncTime: &now,
			}}, state...)

			helmRepo.Status.SpecHash = helmRepo.HashSpec()
			helmRepo.Status.State = appv1alpha2.RepoStateSuccessful
		}

		helmRepo.Status.LastUpdateTime = &now
		if len(state) > HelmRepoSyncStateLen {
			state = state[0:HelmRepoSyncStateLen]
		}
		helmRepo.Status.SyncState = state

		if err := r.Client.Status().Update(ctx, helmRepo); err != nil {
			klog.Errorf("update status failed, error: %s", err)
			return reconcile.Result{RequeueAfter: MinRetryDuration * time.Second}, err
		} else {
			retryAfter = HelmRepoMinSyncPeriod
			if syncErr == nil {
				retryAfter = helmRepo.Spec.SyncPeriod
			}
		}
	} else {
		retryAfter = after
	}

	return reconcile.Result{RequeueAfter: time.Duration(retryAfter) * time.Second}, nil
}

// needReSyncNow checks instance whether need resync now
// if resync is true, it should resync not
// if resync is false and after > 0, it should resync in after seconds
func needReSyncNow(helmRepo *appv1alpha2.HelmRepo) (syncNow bool, after int) {
	now := time.Now()
	if helmRepo.Status.SyncState == nil || len(helmRepo.Status.SyncState) == 0 {
		return true, 0
	}

	states := helmRepo.Status.SyncState

	failedTimes := 0
	for i := range states {
		if states[i].State != appv1alpha2.RepoStateSuccessful {
			failedTimes += 1
		} else {
			break
		}
	}

	state := states[0]

	if helmRepo.Status.SpecHash != helmRepo.HashSpec() && failedTimes == 0 {
		// repo has a successful synchronization
		diff := now.Sub(state.SyncTime.Time) / time.Second
		if diff > 0 && diff < MinRetryDuration {
			return false, int(math.Max(10, float64(MinRetryDuration-diff)))
		} else {
			return true, 0
		}
	}

	period := 0
	if state.State != appv1alpha2.RepoStateSuccessful {
		period = MinRetryDuration * failedTimes
		if period > MaxRetryDuration {
			period = MaxRetryDuration
		}
		if now.After(state.SyncTime.Add(time.Duration(period) * time.Second)) {
			return true, 0
		}
	} else {
		period = helmRepo.Spec.SyncPeriod
		if period != 0 {
			period = int(math.Max(float64(helmRepo.Spec.SyncPeriod), HelmRepoMinSyncPeriod))
			if now.After(state.SyncTime.Add(time.Duration(period) * time.Second)) {
				return true, 0
			}
		} else {
			// need not to sync
			return false, 0
		}
	}

	after = int(state.SyncTime.Time.Add(time.Duration(period) * time.Second).Sub(now).Seconds())

	// may be less than 10 second
	if after <= 10 {
		after = 10
	}
	return false, after
}

func (r *HelmRepoReconciler) syncRepo(ctx context.Context, helmRepo *appv1alpha2.HelmRepo) error {
	// 1. load index from helm repo
	index, err := helmrepoindex.LoadRepoIndex(helmRepo.Spec.Url, &helmRepo.Spec.Credential)
	if err != nil {
		klog.Errorf("load index failed, repo: %s, url: %s, err: %s", helmRepo.GetName(), helmRepo.Spec.Url, err)
		return err
	}

	// 2. sync app and appVersion based on index
	if err := r.syncAppAndAppVersion(ctx, helmRepo, index); err != nil {
		return err
	}

	return nil
}

func (r *HelmRepoReconciler) syncAppAndAppVersion(ctx context.Context,
	repo *appv1alpha2.HelmRepo, index *helmrepo.IndexFile) error {

	savedAppList := &appv1alpha2.ApplicationList{}
	if err := r.List(ctx, savedAppList, client.MatchingLabels{appv1alpha2.RepoIDLabelKey: repo.Name}); err != nil {
		return err
	}
	savedAppMap := make(map[string]*appv1alpha2.Application)
	for _, i := range savedAppList.Items {
		savedAppMap[string(i.Spec.DisplayName[corev1alpha1.DefaultLanguageCode])] = &i
	}

	for name, versions := range index.Entries {
		if len(versions) == 0 {
			continue
		}
		// add new app, appVersion
		if app, exists := savedAppMap[name]; !exists {
			// create app
			app = newApp(*versions[0])
			app.Labels = map[string]string{
				appv1alpha2.RepoIDLabelKey:  repo.Name,
				constants.WorkspaceLabelKey: repo.GetWorkspace(),
			}
			app.Name = generateAppId(repo.Name, name)

			if err := controllerutil.SetControllerReference(repo, app, scheme.Scheme); err != nil {
				klog.Errorf("%s SetControllerReference failed, err:%v", app.Name, err)
				return err
			}
			if err := r.Create(ctx, app); err != nil {
				klog.Errorf("failed create app %s, err:%v", app.Name, err)
				return err
			}

			// create appVersion
			for _, ver := range versions {
				if err := r.createAppVersionAndRefData(ctx, repo, app, ver); err != nil {
					return err
				}
			}
		} else {
			// update exists app information to the latest app version
			overrideAppSpec(app, *versions[len(versions)-1])
			if err := r.Update(ctx, app); err != nil {
				klog.Errorf("failed update app %s, err:%v", app.Name, err)
				return err
			}

			savedAppVersionList := &appv1alpha2.ApplicationVersionList{}
			if err := r.List(ctx, savedAppVersionList, client.MatchingLabels{appv1alpha2.RepoIDLabelKey: repo.Name}); err != nil {
				return err
			}
			savedVersionSet := sets.New[string]()
			for _, i := range savedAppVersionList.Items {
				savedVersionSet.Insert(i.Spec.Version)
			}

			newVersionSet := sets.New[string]()
			for _, ver := range versions {
				// add new chart version
				if !savedVersionSet.Has(ver.Version) {
					if err := r.createAppVersionAndRefData(ctx, repo, app, ver); err != nil {
						return err
					}
				}
				newVersionSet.Insert(ver.Version)
			}

			// delete not exists old app version
			for _, item := range savedAppVersionList.Items {
				if !newVersionSet.Has(item.Spec.Version) {
					if err := r.Delete(ctx, &item); err != nil {
						klog.Errorf("failed delete app version %s, err:%v", item.Name, err)
						return err
					}
				}
			}
		}

	}
	return nil
}

func (r *HelmRepoReconciler) createAppVersionAndRefData(ctx context.Context,
	repo *appv1alpha2.HelmRepo, app *appv1alpha2.Application, ver *helmrepo.ChartVersion) error {

	appVersion := newAppVersion(*ver)
	appVersion.Name = generateAppVersionId(repo.Name, ver.Name, ver.Version)
	appVersion.Labels = map[string]string{
		appv1alpha2.RepoIDLabelKey:  repo.Name,
		appv1alpha2.AppIDLabelKey:   app.Name,
		constants.WorkspaceLabelKey: repo.GetWorkspace(),
	}

	// load chart and store in configmap
	if len(appVersion.Spec.ChartAdditionData.URLs) == 0 {
		return fmt.Errorf("%s chart URLs is empty, unable to get chart data", appVersion.Name)
	}

	url := appVersion.Spec.ChartAdditionData.URLs[0]
	if !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")) {
		url = repo.Spec.Url + "/" + url
	}

	buf, err := helmrepoindex.LoadChart(url, &repo.Spec.Credential)
	if err != nil {
		klog.Errorf("load chart failed, error: %s", err)
		return err
	}

	cm := &corev1.ConfigMap{}
	cm.Name = appVersion.Name
	cm.Namespace = defaultNamespace
	dataKey := fmt.Sprintf("%s-%s.tgz", ver.Name, ver.Version)
	cm.BinaryData = map[string][]byte{dataKey: buf.Bytes()}

	appVersion.Spec.DataRef = &appv1alpha2.ConfigMapKeyRef{
		Namespace: cm.Namespace,
		ConfigMapKeySelector: corev1.ConfigMapKeySelector{
			Key:                  dataKey,
			LocalObjectReference: corev1.LocalObjectReference{Name: cm.Name},
		},
	}

	if err := controllerutil.SetControllerReference(app, appVersion, scheme.Scheme); err != nil {
		klog.Errorf("%s SetControllerReference failed, err:%v", appVersion.Name, err)
		return err
	}
	if err := r.Create(ctx, appVersion); err != nil {
		klog.Errorf("failed create app version %s, err:%v", appVersion.Name, err)
		return err
	}

	if err := controllerutil.SetControllerReference(appVersion, cm, scheme.Scheme); err != nil {
		klog.Errorf("%s SetControllerReference failed, err:%v", cm.Name, err)
		return err
	}
	if err := r.Create(ctx, cm); err != nil {
		klog.Errorf("failed create configmap %s, err:%v", cm.Name, err)
		return err
	}

	return nil
}

func newApp(chartVersion helmrepo.ChartVersion) *appv1alpha2.Application {
	return &appv1alpha2.Application{
		Spec: appv1alpha2.ApplicationSpec{
			DisplayName: corev1alpha1.NewLocales(chartVersion.Name, chartVersion.Name),
			Description: corev1alpha1.NewLocales(chartVersion.Description, chartVersion.Description),
			Icon:        chartVersion.Icon,
			AppHome:     chartVersion.Home,
		},
	}
}

func overrideAppSpec(app *appv1alpha2.Application, chartVersion helmrepo.ChartVersion) {
	app.Spec = appv1alpha2.ApplicationSpec{
		DisplayName: corev1alpha1.NewLocales(chartVersion.Name, chartVersion.Name),
		Description: corev1alpha1.NewLocales(chartVersion.Description, chartVersion.Description),
		Icon:        chartVersion.Icon,
		AppHome:     chartVersion.Home,
	}
}

func newAppVersion(chartVersion helmrepo.ChartVersion) *appv1alpha2.ApplicationVersion {
	version := &appv1alpha2.ApplicationVersion{
		Spec: appv1alpha2.ApplicationVersionSpec{
			AppClassName: defaultAppClassName,
			DataRef:      &appv1alpha2.ConfigMapKeyRef{},
			Metadata: &appv1alpha2.Metadata{
				DisplayName: corev1alpha1.NewLocales(chartVersion.Name, chartVersion.Name),
				Version:     chartVersion.Version,
				Home:        chartVersion.Home,
				Icon:        chartVersion.Icon,
				Description: corev1alpha1.NewLocales(chartVersion.Description, chartVersion.Description),
				Sources:     chartVersion.Sources,
				Keywords:    chartVersion.Keywords,
				Maintainers: make([]*appv1alpha2.Maintainer, 0),
			},
			Created: &metav1.Time{Time: time.Now()},
			ChartAdditionData: &appv1alpha2.ChartAdditionData{
				ChartVersion: chartVersion.Version,
				URLs:         chartVersion.URLs,
				Digest:       chartVersion.Digest,
			},
		},
	}
	for _, v := range chartVersion.Maintainers {
		version.Spec.Maintainers = append(version.Spec.Maintainers, &appv1alpha2.Maintainer{
			Name:  v.Name,
			Email: v.Email,
			URL:   v.URL,
		})
	}

	return version
}

// IsBuiltInRepo checks whether a repo is a built-in repo.
// All the built-in repos are located in the workspace system-workspace and the name starts with 'built-in'
// to differentiate from the repos created by the user.
func IsBuiltInRepo(repoName string) bool {
	return strings.HasPrefix(repoName, appv1alpha2.BuiltinRepoPrefix)
}

// The app version id will be added to the labels of the helm release.
// But the apps in the repos which are created by the user may contain malformed text, so we generate a random name for them.
// The apps in the system repo have been audited by the admin, so the name of the charts should not include malformed text.
// Then we can add the name string to the labels of the k8s object.
func generateAppVersionId(repoName, chartName, version string) string {
	ret := fmt.Sprintf("%s%s-%s-%s", appv1alpha2.AppVersionIDPrefix, repoName, chartName, version)
	if IsBuiltInRepo(repoName) {
		return ret
	}
	return idutils.GetUuid36(ret)
}

func generateAppId(repoName, chartName string) string {
	ret := fmt.Sprintf("%s%s-%s", v1alpha2.AppIDPrefix, repoName, chartName)
	if IsBuiltInRepo(repoName) {
		return ret
	}
	return idutils.GetUuid36(ret)
}
