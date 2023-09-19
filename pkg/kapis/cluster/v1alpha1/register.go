/*
Copyright 2020 KubeSphere Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"net/http"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
)

const (
	GroupName = "cluster.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

func NewHandler(cacheClient runtimeclient.Client) rest.Handler {
	return &handler{
		client: cacheClient,
	}
}

func NewFakeHandler() rest.Handler {
	return &handler{}
}

func (h *handler) AddToContainer(container *restful.Container) error {
	webservice := runtime.NewWebService(GroupVersion)
	// TODO use validating admission webhook instead
	webservice.Route(webservice.POST("/clusters/validation").
		To(h.validateCluster).
		Deprecate().
		Doc("Cluster validation").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagMultiCluster}).
		Reads(clusterv1alpha1.Cluster{}).
		Returns(http.StatusOK, api.StatusOK, nil))

	webservice.Route(webservice.PUT("/clusters/{cluster}/kubeconfig").
		To(h.updateKubeConfig).
		Doc("Update kubeconfig").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagMultiCluster}).
		Param(webservice.PathParameter("cluster", "The specified cluster.").Required(true)).
		Returns(http.StatusOK, api.StatusOK, nil))

	container.Add(webservice)
	return nil
}
