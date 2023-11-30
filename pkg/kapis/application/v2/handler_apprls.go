package v2

import (
	"encoding/json"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
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
	var createRlsRequest appv2.ApplicationRelease
	err := req.ReadEntity(&createRlsRequest)
	if requestDone(err, resp) {
		return
	}

	apprls := appv2.ApplicationRelease{}
	apprls.Name = createRlsRequest.Name
	mutateFn := func() error {
		createRlsRequest.DeepCopyInto(&apprls)
		return nil
	}
	_, err = controllerutil.CreateOrUpdate(req.Request.Context(), h.client, &apprls, mutateFn)
	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeAppRls(req *restful.Request, resp *restful.Response) {
	applicationId := req.PathParameter("application")

	key := runtimeclient.ObjectKey{Name: applicationId}
	app := &appv2.ApplicationRelease{}
	err := h.client.Get(req.Request.Context(), key, app)
	if requestDone(err, resp) {
		return
	}
	app.SetManagedFields(nil)
	// When querying, return real-time data for editing to solve the problem of out-of-sync values during editing.
	// It is only valid when using the ks API query API. If you want kubectl to work, please use the aggregation API for resource abstraction.
	if app.Spec.AppType == appv2.AppTypeYaml || app.Spec.AppType == appv2.AppTypeEdge {
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

	clusterName := app.GetRlsCluster()

	client, err := h.clusterClient.GetRuntimeClient(clusterName)
	if err != nil {
		return nil, err
	}
	clusterClient, err := h.clusterClient.GetClusterClient(clusterName)
	if err != nil {
		return nil, err
	}

	DynamicClient, err := dynamic.NewForConfig(clusterClient.RestConfig)
	if err != nil {
		return nil, err
	}

	jsonList := application.ReadYaml(app.Spec.Values)
	var realTimeDataList []json.RawMessage
	for _, i := range jsonList {
		gvr, utd, err := application.GetInfoFromBytes(i, client.RESTMapper())
		if err != nil {
			return nil, err
		}
		utdRealTime, err := DynamicClient.Resource(gvr).Get(context.Background(), utd.GetName(), metav1.GetOptions{})
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

	app := &appv2.ApplicationRelease{}
	app.Name = applicationId

	err := h.client.Delete(req.Request.Context(), app)

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) ListAppRls(req *restful.Request, resp *restful.Response) {
	labelValues := map[string]string{
		appv2.AppIDLabelKey:           req.QueryParameter("appID"),
		constants.NamespaceLabelKey:   req.PathParameter("namespace"),
		constants.WorkspaceLabelKey:   req.PathParameter("workspace"),
		constants.ClusterNameLabelKey: req.PathParameter("cluster"),
	}
	labelSet := labels.Set{}
	for key, value := range labelValues {
		if value != "" {
			labelSet[key] = value
		}
	}

	opt := runtimeclient.ListOptions{LabelSelector: labels.SelectorFromSet(labelSet)}
	appList := appv2.ApplicationReleaseList{}
	err := h.client.List(req.Request.Context(), &appList, &opt)

	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(&appList, req))
}
