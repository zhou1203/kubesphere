package v2

import (
	"bytes"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/emicklei/go-restful/v3"
	"golang.org/x/net/context"
	"helm.sh/helm/v3/pkg/action"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	"kubesphere.io/api/constants"
	"kubesphere.io/utils/helm"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ksconstants "kubesphere.io/kubesphere/pkg/constants"

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
	list := []string{appv2.AppIDLabelKey, constants.ClusterNameLabelKey, constants.WorkspaceLabelKey, constants.NamespaceLabelKey}
	for _, i := range list {
		value, ok := createRlsRequest.GetLabels()[i]
		if !ok || value == "" {
			err = errors.New("must set %s", i)
			api.HandleBadRequest(resp, nil, err)
			return
		}
	}

	apprls := appv2.ApplicationRelease{}
	apprls.Name = createRlsRequest.Name

	if h.CheckExisted(req, runtimeclient.ObjectKey{Name: apprls.Name}, &apprls) {
		api.HandleConflict(resp, req, fmt.Errorf("apprls %s already exists", apprls.Name))
		return
	}

	if createRlsRequest.Spec.AppType != appv2.AppTypeHelm {
		runtimeClient, _, _, err := h.getCluster(&apprls)
		if requestDone(err, resp) {
			return
		}
		configMap := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Namespace: ksconstants.KubeSphereNamespace,
			Name:      createRlsRequest.Spec.AppVersionID,
		}
		err = h.client.Get(context.TODO(), key, configMap)
		if requestDone(err, resp) {
			return
		}
		template := configMap.BinaryData[appv2.BinaryKey]
		_, err = application.ComplianceCheck(createRlsRequest.Spec.Values, template,
			runtimeClient.RESTMapper(), createRlsRequest.GetRlsNamespace())
		if requestDone(err, resp) {
			return
		}
	}

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
	ctx := req.Request.Context()

	key := runtimeclient.ObjectKey{Name: applicationId}
	app := &appv2.ApplicationRelease{}
	err := h.client.Get(ctx, key, app)
	if requestDone(err, resp) {
		return
	}
	app.SetManagedFields(nil)
	if app.Spec.AppType == appv2.AppTypeYaml || app.Spec.AppType == appv2.AppTypeEdge {
		data, err := h.getRealTimeYaml(ctx, app)
		if err != nil {
			klog.Errorf(err.Error())
			api.HandleInternalError(resp, nil, err)
			return
		}
		app.Status.RealTimeResources = data
	} else {
		data, err := h.getRealTimeHelm(ctx, app)
		if err != nil {
			klog.Errorf(err.Error())
			api.HandleInternalError(resp, nil, err)
			return
		}
		app.Status.RealTimeResources = data
	}

	resp.WriteEntity(app)
}

func (h *appHandler) getRealTimeYaml(ctx context.Context, app *appv2.ApplicationRelease) (data []json.RawMessage, err error) {
	runtimeClient, dynamicClient, cluster, err := h.getCluster(app)
	if err != nil {
		return nil, err
	}

	jsonList, err := application.ReadYaml(app.Spec.Values)
	if err != nil {
		return nil, err
	}
	for _, i := range jsonList {
		gvr, utd, err := application.GetInfoFromBytes(i, runtimeClient.RESTMapper())
		if err != nil {
			return nil, err
		}
		var utdRealTime *unstructured.Unstructured
		utdRealTime, err = dynamicClient.Resource(gvr).Namespace(utd.GetNamespace()).
			Get(ctx, utd.GetName(), metav1.GetOptions{})
		if err != nil {
			klog.Errorf("cluster: %s url: %s resource: %s/%s/%s: %s",
				cluster.Name, cluster.Spec.Connection.KubernetesAPIEndpoint, utd.GetNamespace(), gvr.Resource, utd.GetName(), err)
			realTimeJson := errorRealTime(utd, err.Error())
			data = append(data, realTimeJson)
			continue
		}
		utdRealTime.SetManagedFields(nil)
		realTimeJson, err := utdRealTime.MarshalJSON()
		if err != nil {
			return nil, err
		}
		data = append(data, realTimeJson)
	}

	return data, err
}

func (h *appHandler) getRealTimeHelm(ctx context.Context, app *appv2.ApplicationRelease) (data []json.RawMessage, err error) {
	runtimeClient, dynamicClient, cluster, err := h.getCluster(app)
	if err != nil {
		return nil, err
	}
	helmConf, err := helm.InitHelmConf(string(cluster.Spec.Connection.KubeConfig), app.GetRlsNamespace())
	if err != nil {
		return nil, err
	}
	rel, err := action.NewGet(helmConf).Run(app.Name)
	if err != nil {
		return nil, err
	}
	resources, _ := helmConf.KubeClient.Build(bytes.NewBufferString(rel.Manifest), true)

	for _, i := range resources {
		utd, err := application.ConvertToUnstructured(i.Object)
		if err != nil {
			return nil, err
		}
		marshalJSON, err := utd.MarshalJSON()
		if err != nil {
			return nil, err
		}
		gvr, utd, err := application.GetInfoFromBytes(marshalJSON, runtimeClient.RESTMapper())
		if err != nil {
			return nil, err
		}
		utdRealTime, err := dynamicClient.Resource(gvr).Namespace(utd.GetNamespace()).
			Get(ctx, utd.GetName(), metav1.GetOptions{})
		if err != nil {
			klog.Errorf("cluster: %s url: %s resource: %s/%s/%s: %s",
				cluster.Name, cluster.Spec.Connection.KubernetesAPIEndpoint, utd.GetNamespace(), gvr.Resource, utd.GetName(), err)
			realTimeJson := errorRealTime(utd, err.Error())
			data = append(data, realTimeJson)
			continue
		}
		utdRealTime.SetManagedFields(nil)
		realTimeJson, err := utdRealTime.MarshalJSON()
		if err != nil {
			return nil, err
		}
		data = append(data, realTimeJson)
	}
	return data, nil
}

func errorRealTime(utd *unstructured.Unstructured, msg string) json.RawMessage {
	fake := &unstructured.Unstructured{}
	fake.SetKind(utd.GetKind())
	fake.SetAPIVersion(utd.GetAPIVersion())
	fake.SetName(utd.GetName())
	fake.SetNamespace(utd.GetNamespace())
	fake.SetLabels(utd.GetLabels())
	fake.SetAnnotations(utd.GetAnnotations())
	unstructured.SetNestedField(fake.Object, msg, "status", "state")
	marshalJSON, _ := fake.MarshalJSON()

	return marshalJSON
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
