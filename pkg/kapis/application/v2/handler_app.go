package v2

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"kubesphere.io/kubesphere/pkg/utils/sliceutil"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/emicklei/go-restful/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/constants"

	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/server/params"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateApp(req *restful.Request, resp *restful.Response) {
	createAppRequest := application.AppRequest{}
	err := req.ReadEntity(&createAppRequest)
	if requestDone(err, resp) {
		return
	}

	workspace := req.PathParameter("workspace")
	if workspace == "" {
		workspace = appv2.SystemWorkspace
	}
	appRequest, vRequest, err := parseRequest(createAppRequest, workspace)
	if requestDone(err, resp) {
		return
	}

	validate, _ := strconv.ParseBool(req.QueryParameter("validate"))
	if validate {
		rv := ValidatePackage(appRequest)
		resp.WriteAsJson(rv)
		return
	}

	v := []application.AppRequest{vRequest}
	err = application.CreateOrUpdateApp(req.Request.Context(), h.client, appRequest, v)
	if requestDone(err, resp) {
		return
	}
	data := map[string]interface{}{
		"appID":     appRequest.AppName,
		"versionID": vRequest.VersionName,
	}

	resp.WriteAsJson(data)
}

type ValidatePackageResponse struct {
	Description string
	Name        string
	VersionName string
}

func ValidatePackage(req application.AppRequest) (result application.AppRequest) {

	result.AppName = req.AppName
	result.VersionName = req.VersionName
	result.Description = req.Description
	result.Icon = req.Icon

	return result
}

func helmRequest(chartPack *chart.Chart, workspace string, data []byte) (appRequest, vRequest application.AppRequest) {

	appRequest = application.AppRequest{
		RepoName:    "upload",
		AppName:     chartPack.Metadata.Name,
		Icon:        chartPack.Metadata.Icon,
		AppHome:     chartPack.Metadata.Home,
		AppType:     appv2.AppTypeHelm,
		Workspace:   workspace,
		VersionName: chartPack.Metadata.Version,
	}

	vRequest = application.AppRequest{
		RepoName:    "upload",
		VersionName: chartPack.Metadata.Version,
		AppName:     appRequest.AppName,
		AppHome:     chartPack.Metadata.Home,
		Icon:        chartPack.Metadata.Icon,
		Package:     data,
		AppType:     appv2.AppTypeHelm,
		Workspace:   workspace,
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
	}

	return appRequest, vRequest
}

func (h *appHandler) ListApps(req *restful.Request, resp *restful.Response) {
	workspace := req.PathParameter("workspace")
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

	filtered := appv2.ApplicationList{}
	for _, app := range result.Items {
		curApp := app
		if conditions.Match[Status] != "" {
			states := strings.Split(conditions.Match[Status], "|")
			state := curApp.Status.State
			if !sliceutil.HasString(states, state) {
				continue
			}
		}
		filtered.Items = append(filtered.Items, curApp)
	}

	resp.WriteEntity(convertToListResult(&filtered, req))
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
	ctx := req.Request.Context()

	user, _ := request.UserFrom(req.Request.Context())
	if user != nil {
		doActionRequest.UserName = user.GetName()
	}
	app := &appv2.Application{}
	app.Name = req.PathParameter("app")

	err = h.client.Get(ctx, runtimeclient.ObjectKey{Name: app.Name}, app)
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
		h.client.List(ctx, appVersionList, opt)
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
		h.client.Update(ctx, app)
	}

	app.Status.State = doActionRequest.State
	app.Status.UpdateTime = &metav1.Time{Time: time.Now()}
	err = h.client.Status().Update(ctx, app)
	if requestDone(err, resp) {
		return
	}

	//update appversion status
	versions := &appv2.ApplicationVersionList{}

	opt := &runtimeclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{appv2.AppIDLabelKey: app.Name}),
	}

	err = h.client.List(ctx, versions, opt)
	if requestDone(err, resp) {
		return
	}
	for _, version := range versions.Items {
		if version.Status.State == appv2.StatusActive || version.Status.State == appv2.ReviewStatusSuspended {
			err = DoAppVersionAction(ctx, version.Name, doActionRequest, h.client)
			if err != nil {
				klog.V(4).Infoln(err)
				api.HandleInternalError(resp, nil, err)
				return
			}
		}
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) PatchApp(req *restful.Request, resp *restful.Response) {
	var err error
	appId := req.PathParameter("app")
	appBody := &application.AppRequest{}
	err = req.ReadEntity(appBody)
	if requestDone(err, resp) {
		return
	}
	ctx := req.Request.Context()

	app := &appv2.Application{}
	app.Name = appId
	err = h.client.Get(ctx, runtimeclient.ObjectKey{Name: app.Name}, app)
	if requestDone(err, resp) {
		return
	}
	if app.GetLabels() == nil {
		app.SetLabels(map[string]string{})
	}
	if appBody.CategoryName != "" {
		app.Labels[appv2.AppCategoryNameKey] = appBody.CategoryName
	}

	if appBody.Icon != "" {
		app.Spec.Icon = appBody.Icon
	}
	ant := app.GetAnnotations()
	if ant == nil {
		ant = make(map[string]string)
	}

	if appBody.Description != "" {
		ant[constants.DescriptionAnnotationKey] = appBody.Description
		app.SetAnnotations(ant)
	}
	if appBody.AliasName != "" {
		ant[constants.DisplayNameAnnotationKey] = appBody.AliasName
		app.SetAnnotations(ant)
	}

	err = h.client.Update(ctx, app)
	if requestDone(err, resp) {
		return
	}

	resp.WriteAsJson(app)
}
