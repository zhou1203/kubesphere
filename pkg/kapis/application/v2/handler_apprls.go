package v2

import (
	"encoding/json"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	"kubesphere.io/api/constants"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

func (h *appHandler) CreateOrUpdateAppRls(req *restful.Request, resp *restful.Response) {
	namespace := req.PathParameter("namespace")
	workspace := req.PathParameter("workspace")
	cluster := req.PathParameter("cluster")
	var createRlsRequest appv2.ApplicationRelease
	err := req.ReadEntity(&createRlsRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}

	apprls := appv2.ApplicationRelease{}
	apprls.Name = createRlsRequest.Name
	mutateFn := func() error {
		apprls.Spec.AppID = createRlsRequest.Spec.AppID
		apprls.Spec.AppVersionID = createRlsRequest.Spec.AppVersionID
		apprls.Spec.AppType = createRlsRequest.Spec.AppType
		apprls.Spec.Values = createRlsRequest.Spec.Values
		apprls.SetLabels(map[string]string{
			constants.NamespaceLabelKey:   namespace,
			constants.ClusterNameLabelKey: cluster,
			constants.WorkspaceLabelKey:   workspace,
		})
		return nil
	}
	_, err = controllerutil.CreateOrUpdate(req.Request.Context(), h.client, &apprls, mutateFn)
	if err != nil {
		klog.Errorln(err)
		api.HandleInternalError(resp, nil, err)
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeAppRls(req *restful.Request, resp *restful.Response) {
	applicationId := req.PathParameter("application")
	namespace := req.PathParameter("namespace")

	key := runtimeclient.ObjectKey{Name: applicationId, Namespace: namespace}
	app := &appv2.ApplicationRelease{}
	err := h.client.Get(req.Request.Context(), key, app)
	if requestDone(err, resp) {
		return
	}
	app.SetManagedFields(nil)
	// When querying, return real-time data for editing to solve the problem of out-of-sync values during editing.
	// It is only valid when using the ks API query API. If you want kubectl to work, please use the aggregation API for resource abstraction.
	if app.Spec.AppType == appv2.AppTypeYaml {
		data, err := h.getRealTimeObj(app)
		if err != nil {
			klog.V(4).Infoln(err)
			api.HandleInternalError(resp, nil, err)
		}
		app.Spec.Values = data

	}
	resp.WriteEntity(app)
}

func (h *appHandler) getRealTimeObj(app *appv2.ApplicationRelease) ([]byte, error) {
	jsonList := application.ReadYaml(app.Spec.Values)
	var realTimeDataList []json.RawMessage
	for _, i := range jsonList {
		gvr, utd, err := application.GetInfoFromBytes(i, h.client.RESTMapper())
		if err != nil {
			return nil, err
		}
		utdRealTime, err := h.dynamicClient.Resource(gvr).Namespace(utd.GetNamespace()).
			Get(context.Background(), utd.GetName(), metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		utdRealTime.SetManagedFields(nil)

		realTimeData, err := utdRealTime.MarshalJSON()
		if err != nil {
			return nil, err
		}
		realTimeDataList = append(realTimeDataList, realTimeData)
	}
	data, err := application.ConvertJSONToYAMLList(realTimeDataList)
	return data, err
}

func (h *appHandler) DeleteAppRls(req *restful.Request, resp *restful.Response) {
	applicationId := req.PathParameter("application")
	namespace := req.PathParameter("namespace")

	app := &appv2.ApplicationRelease{}
	app.Namespace = namespace
	app.Name = applicationId

	err := h.client.Delete(req.Request.Context(), app)

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) ListAppRls(req *restful.Request, resp *restful.Response) {

	appList := appv2.ApplicationReleaseList{}
	err := h.client.List(req.Request.Context(), &appList)

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(&appList, req))
}
