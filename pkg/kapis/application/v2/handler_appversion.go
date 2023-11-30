package v2

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/chart/loader"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/utils/sliceutil"

	"kubesphere.io/kubesphere/pkg/server/params"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateAppVersion(req *restful.Request, resp *restful.Response) {
	var createAppVersionRequest application.AppRequest
	err := req.ReadEntity(&createAppVersionRequest)
	if requestDone(err, resp) {
		return
	}

	createAppVersionRequest.AppName = req.PathParameter("app")
	app := appv2.Application{}
	err = h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{Name: createAppVersionRequest.AppName}, &app)
	if requestDone(err, resp) {
		return
	}

	workspace := req.PathParameter("workspace")
	if workspace == "" {
		workspace = appv2.SystemWorkspace
	}

	_, vRequest, err := parseRequest(createAppVersionRequest, workspace)
	if requestDone(err, resp) {
		return
	}

	err = application.CreateOrUpdateAppVersion(context.TODO(), h.client, app, vRequest)
	if requestDone(err, resp) {
		return
	}
	data := map[string]interface{}{
		"versionID": vRequest.VersionName,
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
		"versionID": versionId,
		"package":   configMap.BinaryData[appv2.BinaryKey],
		"appID":     app,
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

	if configMap.Labels["appType"] == appv2.AppTypeYaml || configMap.Labels["appType"] == appv2.AppTypeEdge {
		data["all.yaml"] = configMap.BinaryData[appv2.BinaryKey]
		resp.WriteAsJson(data)
		return
	}

}

func (h *appHandler) AppVersionAction(req *restful.Request, resp *restful.Response) {
	versionID := req.PathParameter("version")
	var doActionRequest appv2.ApplicationVersionStatus
	err := req.ReadEntity(&doActionRequest)
	if requestDone(err, resp) {
		return
	}

	appVersion := &appv2.ApplicationVersion{}
	err = h.client.Get(context.Background(), runtimeclient.ObjectKey{Name: versionID}, appVersion)
	if requestDone(err, resp) {
		return
	}

	// app version check state draft -> submitted -> (rejected -> submitted ) -> passed-> active -> (suspended -> active), draft -> submitted -> active
	switch doActionRequest.State {
	case appv2.ReviewStatusSubmitted:
		if appVersion.Status.State != appv2.ReviewStatusDraft {
			err = fmt.Errorf("app %s is not in draft status", appVersion.Name)
		}
	case appv2.ReviewStatusPassed, appv2.ReviewStatusRejected:
		if appVersion.Status.State != appv2.ReviewStatusSubmitted {
			err = fmt.Errorf("app %s is not in submitted status", appVersion.Name)
		}
	case appv2.ReviewStatusActive:
		if appVersion.Status.State != appv2.ReviewStatusPassed &&
			appVersion.Status.State != appv2.ReviewStatusSuspended &&
			appVersion.Status.State != appv2.ReviewStatusSubmitted {
			err = fmt.Errorf("app %s is not in passed or suspended or submitted status", appVersion.Name)
		}
	case appv2.ReviewStatusSuspended:
		if appVersion.Status.State != appv2.ReviewStatusActive {
			err = fmt.Errorf("app %s is not in active status", appVersion.Name)
		}
	}
	if requestDone(err, resp) {
		return
	}

	err = DoAppVersionAction(versionID, doActionRequest, h.client)
	if requestDone(err, resp) {
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
	version.Status.Updated = &metav1.Time{Time: metav1.Now().Time}
	err = client.Status().Update(context.TODO(), version)

	return err
}

func (h *appHandler) ListReviews(req *restful.Request, resp *restful.Response) {
	conditions, err := params.ParseConditions(req)

	if requestDone(err, resp) {
		return
	}

	appVersions := appv2.ApplicationVersionList{}
	err = h.client.List(req.Request.Context(), &appVersions)
	if requestDone(err, resp) {
		return
	}

	if conditions == nil || len(conditions.Match) == 0 {
		resp.WriteEntity(convertToListResult(&appVersions, req))
		return
	}

	filteredAppVersions := appv2.ApplicationVersionList{}
	for _, version := range appVersions.Items {
		curVersion := version
		if conditions.Match[Status] != "" {
			states := strings.Split(conditions.Match[Status], "|")
			state := curVersion.Status.State
			if !sliceutil.HasString(states, state) {
				continue
			}
		}
		filteredAppVersions.Items = append(filteredAppVersions.Items, curVersion)
	}

	resp.WriteEntity(convertToListResult(&filteredAppVersions, req))
}
