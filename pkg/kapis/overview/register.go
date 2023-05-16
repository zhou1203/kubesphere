package overview

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/simple/client/overview"
)

func AddToContainer(container *restful.Container, collector overview.Counter) error {
	overviewWS := &restful.WebService{}
	overviewWS.Path("/overview").Produces(restful.MIME_JSON)

	handler := newOverviewHandler(collector)

	overviewWS.Route(overviewWS.GET("").
		To(handler.GetClusterOverview).
		Returns(http.StatusOK, api.StatusOK, overview.MetricResults{}).
		Doc("Get clusters overview"))

	overviewWS.Route(overviewWS.GET("/namespaces/{namespace}").
		To(handler.GetNamespaceOverview).
		Returns(http.StatusOK, api.StatusOK, overview.MetricResults{}).
		Doc("Get namespace overview"))

	container.Add(overviewWS)

	return nil
}
