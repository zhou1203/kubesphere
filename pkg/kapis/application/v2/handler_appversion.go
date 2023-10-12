package v2

import (
	"bytes"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/chart/loader"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateAppVersion(req *restful.Request, resp *restful.Response) {
	var createAppVersionRequest CreateAppVersionRequest
	err := req.ReadEntity(&createAppVersionRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}
	createAppVersionRequest.AppId = req.PathParameter("app")

	app := appv2.Application{}
	err = h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{Name: createAppVersionRequest.AppId}, &app)
	if requestDone(err, resp) {
		return
	}

	var (
		appRequest application.NewAppRequest
		vRequest   []application.VersionRequest
	)
	if createAppVersionRequest.AppType == appv2.AppTypeHelm {
		appRequest, vRequest, err = h.helmRequest(req, createAppVersionRequest.Package)
		if err != nil {
			api.HandleInternalError(resp, nil, err)
			return
		}
	}
	if createAppVersionRequest.AppType == appv2.AppTypeYaml {
		appRequest, vRequest = yamlRequest(
			createAppVersionRequest.AppId,
			createAppVersionRequest.VersionName,
			req.PathParameter("workspace"),
			createAppVersionRequest.Package,
		)
	}

	err = application.CreateOrUpdateAppVersion(context.TODO(), h.client, appRequest, app, vRequest[0])
	if err != nil {
		klog.Errorln(err)
		handleError(resp, err)
		return
	}
	data := map[string]interface{}{
		"version_id": vRequest[0].Version,
	}

	resp.WriteAsJson(data)
}

func (h *appHandler) DeleteAppVersion(req *restful.Request, resp *restful.Response) {
	versionId := req.PathParameter("version")
	err := h.client.Delete(req.Request.Context(), &appv2.ApplicationVersion{ObjectMeta: metav1.ObjectMeta{Name: versionId}})
	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeAppVersion(req *restful.Request, resp *restful.Response) {
	versionId := req.PathParameter("version")
	result := &appv2.ApplicationVersion{}
	err := h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{Name: versionId}, result)
	if requestDone(err, resp) {
		return
	}
	result.SetManagedFields(nil)

	resp.WriteEntity(result)
}

func (h *appHandler) ListAppVersions(req *restful.Request, resp *restful.Response) {

	var err error
	var lbs labels.Selector

	if req.PathParameter("workspace") != "" {
		lbs = labels.SelectorFromSet(labels.Set{
			constants.WorkspaceLabelKey: req.PathParameter("workspace"),
			appv2.AppIDLabelKey:         req.PathParameter("app"),
		})
	} else {
		lbs = labels.SelectorFromSet(labels.Set{
			appv2.AppIDLabelKey: req.PathParameter("app"),
		})
	}

	opt := runtimeclient.ListOptions{LabelSelector: lbs}
	result := appv2.ApplicationVersionList{}
	err = h.client.List(req.Request.Context(), &result, &opt)

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(&result, req))
}

func (h *appHandler) GetAppVersionPackage(req *restful.Request, resp *restful.Response) {
	versionId := req.PathParameter("version")
	app := req.PathParameter("app")

	key := runtimeclient.ObjectKey{Name: versionId, Namespace: constants.KubeSphereNamespace}
	configMap := &v1.ConfigMap{}
	err := h.client.Get(req.Request.Context(), key, configMap)
	if requestDone(err, resp) {
		return
	}
	data := map[string]interface{}{
		"version_id": versionId,
		"package":    configMap.BinaryData[appv2.BinaryKey],
		"app_id":     app,
	}

	resp.WriteAsJson(data)
}

func (h *appHandler) GetAppVersionFiles(req *restful.Request, resp *restful.Response) {
	versionId := req.PathParameter("version")

	key := runtimeclient.ObjectKey{Name: versionId, Namespace: constants.KubeSphereNamespace}
	configMap := &v1.ConfigMap{}
	err := h.client.Get(req.Request.Context(), key, configMap)
	if requestDone(err, resp) {
		return
	}
	data := make(map[string][]byte)

	if configMap.Labels["appType"] == appv2.AppTypeHelm {
		chartData, err := loader.LoadArchive(bytes.NewReader(configMap.BinaryData[appv2.BinaryKey]))
		if err != nil {
			klog.Errorf("Failed to load package for app version: %s, error: %+v", versionId, err)
			api.HandleInternalError(resp, nil, err)
			return
		}
		for _, f := range chartData.Raw {
			data[f.Name] = f.Data
		}
		resp.WriteAsJson(data)
		return
	}

	if configMap.Labels["appType"] == appv2.AppTypeYaml {
		data["all.yaml"] = configMap.BinaryData[appv2.BinaryKey]
		resp.WriteAsJson(data)
		return
	}

}

func (h *appHandler) AppVersionAction(req *restful.Request, resp *restful.Response) {
	var doActionRequest appv2.ApplicationVersionStatus
	err := req.ReadEntity(&doActionRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}
	doActionRequest.UserName = req.HeaderParameter(appv2.UserNameHeader)

	user, _ := request.UserFrom(req.Request.Context())
	if user != nil {
		doActionRequest.UserName = user.GetName()
	}
	versionId := req.PathParameter("version")

	err = DoAppVersionAction(versionId, doActionRequest, h.client)
	if err != nil {
		klog.Errorln(err)
		api.HandleError(resp, nil, err)
		return
	}

	resp.WriteEntity(errors.None)
}

func DoAppVersionAction(versionId string, actionReq appv2.ApplicationVersionStatus, client runtimeclient.Client) error {

	key := runtimeclient.ObjectKey{Name: versionId}
	version := &appv2.ApplicationVersion{}
	err := client.Get(context.TODO(), key, version)
	if err != nil {
		klog.Errorf("get app version %s failed, error: %s", versionId, err)
		return err
	}
	version.Status.State = actionReq.State
	version.Status.Message = actionReq.Message
	version.Status.UserName = actionReq.UserName
	err = client.Status().Update(context.TODO(), version)

	return err
}
