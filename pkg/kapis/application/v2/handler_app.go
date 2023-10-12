package v2

import (
	"bytes"
	"fmt"
	"time"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/chart/loader"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/constants"

	"strconv"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateApp(req *restful.Request, resp *restful.Response) {
	createAppRequest := &CreateAppRequest{}
	err := req.ReadEntity(createAppRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}

	var (
		appRequest application.NewAppRequest
		vRequest   []application.VersionRequest
	)
	if createAppRequest.AppType == appv2.AppTypeHelm {
		appRequest, vRequest, err = h.helmRequest(req, createAppRequest.Package)
		if err != nil {
			api.HandleInternalError(resp, nil, err)
			return
		}
	}
	if createAppRequest.AppType == appv2.AppTypeYaml {
		appRequest, vRequest = yamlRequest(
			createAppRequest.Name,
			createAppRequest.VersionName,
			req.PathParameter("workspace"),
			createAppRequest.Package)
	}

	err = application.CreateOrUpdateApp(context.TODO(), h.client, appRequest, vRequest)
	if err != nil {
		api.HandleInternalError(resp, nil, err)
		return
	}
	data := map[string]interface{}{
		"app_id":     appRequest.AppName,
		"version_id": vRequest[0].Name,
	}

	resp.WriteAsJson(data)
}

func yamlRequest(name, versionName, workspace string, data []byte) (application.NewAppRequest, []application.VersionRequest) {

	appRequest := application.NewAppRequest{
		RepoName:  "configyaml",
		AppName:   fmt.Sprintf("configyaml-%s", name),
		Workspace: workspace,
	}

	vRequest := []application.VersionRequest{{
		RepoName: "configyaml",
		AppName:  appRequest.AppName,
		Name:     fmt.Sprintf("configyaml-%s-%s", name, versionName),
		Version:  versionName,
		Data:     data,
		AppType:  appv2.AppTypeYaml,
	}}

	return appRequest, vRequest
}

func (h *appHandler) helmRequest(req *restful.Request, data []byte) (application.NewAppRequest, []application.VersionRequest, error) {
	validate, _ := strconv.ParseBool(req.QueryParameter("validate"))

	chartPack, err := loader.LoadArchive(bytes.NewReader(data))
	if err != nil {
		klog.Errorf("Failed to load package, error: %+v", err)
		return application.NewAppRequest{}, nil, err
	}
	if validate {
		return application.NewAppRequest{}, nil, nil
	}
	appRequest := application.NewAppRequest{
		RepoName: "upload",
		AppName:  fmt.Sprintf("upload-%s", chartPack.Metadata.Name),
		Icon:     chartPack.Metadata.Icon,
		AppHome:  chartPack.Metadata.Home,
	}

	vRequest := []application.VersionRequest{{
		RepoName: "upload",
		Name:     fmt.Sprintf("upload-%s-%s", chartPack.Metadata.Name, chartPack.AppVersion()),
		AppName:  appRequest.AppName,
		Home:     chartPack.Metadata.Home,
		Version:  chartPack.AppVersion(),
		Icon:     chartPack.Metadata.Icon,
		Data:     data,
		AppType:  appv2.AppTypeHelm,
	}}

	return appRequest, vRequest, nil
}

func (h *appHandler) ListApps(req *restful.Request, resp *restful.Response) {

	var err error
	result := appv2.ApplicationList{}
	if req.PathParameter("workspace") != "" {
		workspace := req.PathParameter("workspace")
		opt := runtimeclient.ListOptions{LabelSelector: labels.SelectorFromSet(
			labels.Set{constants.WorkspaceLabelKey: workspace})}
		err = h.client.List(req.Request.Context(), &result, &opt)
	} else {
		err = h.client.List(req.Request.Context(), &result)
	}

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(&result, req))
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
