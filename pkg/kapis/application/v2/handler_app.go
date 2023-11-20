package v2

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/blang/semver/v4"
	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/chart/loader"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/constants"
	resv1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"

	"strconv"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/server/params"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateApp(req *restful.Request, resp *restful.Response) {
	createAppRequest := &CreateAppRequest{}
	err := req.ReadEntity(createAppRequest)
	if requestDone(err, resp) {
		return
	}

	validate, _ := strconv.ParseBool(req.QueryParameter("validate"))
	if validate {
		if createAppRequest.AppType == appv2.AppTypeHelm {
			chartPack, err := loader.LoadArchive(bytes.NewReader(createAppRequest.Package))
			if requestDone(err, resp) {
				return
			}
			resp.WriteAsJson(chartPack)
			return
		} else {
			resp.WriteEntity(errors.None)
			return
		}
	}

	var (
		appRequest application.NewAppRequest
		vRequest   []application.VersionRequest
	)

	workspace := req.PathParameter("workspace")
	if workspace == "" {
		workspace = appv2.SystemWorkspace
	}

	if createAppRequest.AppType == appv2.AppTypeHelm {
		chartPack, err := loader.LoadArchive(bytes.NewReader(createAppRequest.Package))
		if requestDone(err, resp) {
			return
		}
		appRequest, vRequest, err = h.helmRequest(chartPack, workspace, createAppRequest.Package)
		if requestDone(err, resp) {
			return
		}
	}
	if createAppRequest.AppType == appv2.AppTypeYaml || createAppRequest.AppType == appv2.AppTypeEdge {
		appRequest, vRequest = yamlRequest(
			createAppRequest.Name,
			createAppRequest.VersionName,
			workspace,
			createAppRequest.AppType,
			createAppRequest.Package,
		)
	}

	appRequest.Description = createAppRequest.Description
	err = application.CreateOrUpdateApp(context.TODO(), h.client, nil, appRequest, vRequest)
	if requestDone(err, resp) {
		return
	}
	data := map[string]interface{}{
		"app_id":     appRequest.AppName,
		"version_id": vRequest[0].Name,
	}

	resp.WriteAsJson(data)
}

func yamlRequest(name, versionName, workspace, appType string, data []byte) (application.NewAppRequest, []application.VersionRequest) {

	appRequest := application.NewAppRequest{
		RepoName:  "configyaml",
		AppName:   name,
		Workspace: workspace,
		AppType:   appType,
	}

	vRequest := []application.VersionRequest{{
		RepoName: "configyaml",
		AppName:  appRequest.AppName,
		Name:     fmt.Sprintf("%s-%s", name, versionName),
		Version:  versionName,
		Data:     data,
		AppType:  appType,
	}}

	return appRequest, vRequest
}

func (h *appHandler) helmRequest(chartPack *chart.Chart, workspace string, data []byte) (application.NewAppRequest, []application.VersionRequest, error) {

	appRequest := application.NewAppRequest{
		RepoName:  "upload",
		AppName:   chartPack.Metadata.Name,
		Icon:      chartPack.Metadata.Icon,
		AppHome:   chartPack.Metadata.Home,
		AppType:   appv2.AppTypeHelm,
		Workspace: workspace,
	}

	vRequest := []application.VersionRequest{{
		RepoName: "upload",
		Name:     fmt.Sprintf("%s-%s", chartPack.Metadata.Name, chartPack.AppVersion()),
		AppName:  appRequest.AppName,
		Home:     chartPack.Metadata.Home,
		Version:  chartPack.AppVersion(),
		Icon:     chartPack.Metadata.Icon,
		Data:     data,
		AppType:  appv2.AppTypeHelm,
		Maintainers: func() []appv2.Maintainer {
			maintainers := make([]appv2.Maintainer, len(chartPack.Metadata.Maintainers))
			for i, maintainer := range chartPack.Metadata.Maintainers {
				maintainers[i] = appv2.Maintainer{
					Name:  maintainer.Name,
					Email: maintainer.Email,
					URL:   maintainer.URL,
				}
			}
			return maintainers
		}(),
	}}

	return appRequest, vRequest, nil
}

func (h *appHandler) ListApps(req *restful.Request, resp *restful.Response) {
	workspace := req.PathParameter("workspace")
	queryParams := query.ParseQueryParameter(req)
	conditions, err := params.ParseConditions(req)
	if requestDone(err, resp) {
		return
	}

	opt := runtimeclient.ListOptions{}
	if workspace != "" {
		opt.LabelSelector = labels.SelectorFromSet(labels.Set{constants.WorkspaceLabelKey: workspace})
	}

	result := appv2.ApplicationList{}
	err = h.client.List(req.Request.Context(), &result, &opt)
	if requestDone(err, resp) {
		return
	}

	// TODO optimize filtered
	filtered := appStatusFilter(result.Items, conditions)
	rawAppList, _, totalCount := resv1beta1.DefaultList(filtered, queryParams, resv1beta1.DefaultCompare, resv1beta1.DefaultFilter)

	for _, v := range rawAppList {
		app := v.(*appv2.Application)
		app.SetManagedFields(nil)

		category := &appv2.Category{}
		err = h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{Name: app.GetCategory()}, category)
		if err != nil {
			if app.Annotations == nil {
				app.Annotations = map[string]string{}
			}
			app.Annotations[appv2.AppCategoryNameKey] = category.Spec.DisplayName.Default()
		}

		opt := runtimeclient.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{appv2.AppIDLabelKey: app.Name})}
		appVersionList := appv2.ApplicationVersionList{}
		h.client.List(req.Request.Context(), &appVersionList, &opt)

		if appVersionList.Items == nil || len(appVersionList.Items) == 0 {
			klog.Info("Failed to get app version list: ", app.Name)
			continue
		}

		// set latest app version and maintainers
		latestAppVersion := "0.0.0"
		pos := 0
		for i, v := range appVersionList.Items {
			parsedVersion, err := semver.Make(v.Spec.Version)
			if err != nil {
				klog.Info("Failed to parse version: ", v.Spec.Version)
				continue
			}
			if parsedVersion.GT(semver.MustParse(latestAppVersion)) {
				latestAppVersion = v.Spec.Version
				pos = i
			}
		}
		app.Annotations[appv2.LatestAppVersionKey] = latestAppVersion
		maintainers := ""
		if appVersionList.Items[pos].Spec.Maintainer != nil {
			for _, m := range appVersionList.Items[pos].Spec.Maintainer {
				maintainers = maintainers + m.Name + ","
			}
			maintainers = strings.TrimRight(maintainers, ",")
		}
		app.Annotations[appv2.AppMaintainersKey] = maintainers
	}

	resp.WriteEntity(api.ListResult{Items: rawAppList, TotalItems: int(*totalCount)})
}

func (h *appHandler) DescribeApp(req *restful.Request, resp *restful.Response) {

	key := runtimeclient.ObjectKey{Name: req.PathParameter("app")}
	app := &appv2.Application{}
	err := h.client.Get(req.Request.Context(), key, app)
	if requestDone(err, resp) {
		return
	}
	app.SetManagedFields(nil)

	resp.WriteEntity(app)
}

func (h *appHandler) DeleteApp(req *restful.Request, resp *restful.Response) {
	appId := req.PathParameter("app")

	err := h.client.Delete(req.Request.Context(), &appv2.Application{ObjectMeta: metav1.ObjectMeta{Name: appId}})

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DoAppAction(req *restful.Request, resp *restful.Response) {
	var doActionRequest appv2.ApplicationVersionStatus
	err := req.ReadEntity(&doActionRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}

	user, _ := request.UserFrom(req.Request.Context())
	if user != nil {
		doActionRequest.UserName = user.GetName()
	}
	app := &appv2.Application{}
	app.Name = req.PathParameter("app")

	err = h.client.Get(context.Background(), runtimeclient.ObjectKey{Name: app.Name}, app)
	if requestDone(err, resp) {
		return
	}

	// app state check, draft -> active -> suspended -> active
	switch doActionRequest.State {
	case appv2.ReviewStatusActive:
		if app.Status.State != appv2.ReviewStatusDraft &&
			app.Status.State != appv2.ReviewStatusSuspended {
			err = fmt.Errorf("app %s is not in draft or suspended status", app.Name)
			break
		}
		// active state is only allowed if at least one app version is in active or passed state
		appVersionList := &appv2.ApplicationVersionList{}
		opt := &runtimeclient.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{appv2.AppIDLabelKey: app.Name})}
		h.client.List(context.Background(), appVersionList, opt)
		okActive := false
		if len(appVersionList.Items) > 0 {
			for _, v := range appVersionList.Items {
				if v.Status.State == appv2.ReviewStatusActive ||
					v.Status.State == appv2.ReviewStatusPassed {
					okActive = true
					break
				}
			}
		}
		if !okActive {
			err = fmt.Errorf("app %s has no active or passed appversion", app.Name)
		}
	case appv2.ReviewStatusSuspended:
		if app.Status.State != appv2.ReviewStatusActive {
			err = fmt.Errorf("app %s is not in active status", app.Name)
		}
	}

	if requestDone(err, resp) {
		return
	}
	if doActionRequest.State == appv2.ReviewStatusActive {
		if app.Labels == nil {
			app.Labels = map[string]string{}
		}

		app.Labels[appv2.AppStoreLabelKey] = "true"
		if app.Labels[appv2.AppCategoryLabelKey] == "" {
			app.Labels[appv2.AppCategoryLabelKey] = appv2.UncategorizedCategoryID
		}
		h.client.Update(context.Background(), app)
	}

	app.Status.State = doActionRequest.State
	app.Status.UpdateTime = &metav1.Time{Time: time.Now()}
	err = h.client.Status().Update(context.Background(), app)
	if requestDone(err, resp) {
		return
	}

	//update appversion status
	versions := &appv2.ApplicationVersionList{}

	opt := &runtimeclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{appv2.AppIDLabelKey: app.Name}),
	}

	err = h.client.List(context.Background(), versions, opt)
	if requestDone(err, resp) {
		return
	}
	for _, version := range versions.Items {
		err = DoAppVersionAction(version.Name, doActionRequest, h.client)
		if err != nil {
			klog.V(4).Infoln(err)
			api.HandleInternalError(resp, nil, err)
			return
		}
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) PatchApp(req *restful.Request, resp *restful.Response) {
	var err error
	appId := req.PathParameter("app")
	appBody := &CreateAppRequest{}
	err = req.ReadEntity(appBody)
	if requestDone(err, resp) {
		return
	}

	app := &appv2.Application{}
	app.Name = appId
	err = h.client.Get(context.Background(), runtimeclient.ObjectKey{Name: app.Name}, app)
	if requestDone(err, resp) {
		return
	}

	if appBody.CategoryID != "" {
		if app.Labels == nil {
			app.Labels = map[string]string{}
		}
		app.Labels[appv2.AppCategoryLabelKey] = appBody.CategoryID
	}

	if appBody.Icon != "" {
		app.Spec.Icon = appBody.Icon
	}

	if appBody.Description != "" {
		app.Spec.Description = corev1alpha1.NewLocales(appBody.Description, appBody.Description)
	}

	err = h.client.Update(context.Background(), app)
	if requestDone(err, resp) {
		return
	}

	resp.WriteAsJson(app)
}
