package overview

import (
	"github.com/emicklei/go-restful/v3"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	. "kubesphere.io/kubesphere/pkg/simple/client/overview"
)

type overviewHandler struct {
	counter Counter
}

var (
	ClusterMetricNames = []string{NamespaceCount, PodCount, DeploymentCount, StatefulSetCount, DaemonSetCount,
		JobCount, CronJobCount, PersistentVolumeCount, ServiceCount, IngressCount, ClusterRoleBindingCount, ClusterRoleCount}

	NamespaceMetricNames = []string{PodCount, DeploymentCount, StatefulSetCount, DaemonSetCount, JobCount, ServiceCount,
		IngressCount, RoleCount, RoleBindingCount}
)

func newOverviewHandler(counter Counter) *overviewHandler {
	return &overviewHandler{counter: counter}
}

func (h *overviewHandler) GetClusterOverview(request *restful.Request, response *restful.Response) {
	metrics, err := h.counter.GetMetrics(ClusterMetricNames, "", "clusters", query.ParseQueryParameter(request))
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	_ = response.WriteEntity(metrics)
}

func (h *overviewHandler) GetNamespaceOverview(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	metrics, err := h.counter.GetMetrics(NamespaceMetricNames, namespace, "namespaces", query.ParseQueryParameter(request))
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	_ = response.WriteEntity(metrics)
}
