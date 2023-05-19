/*
Copyright 2023 KubeSphere Authors

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

package v1alpha2

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	gatewayv1alpha2 "kubesphere.io/api/gateway/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
)

const (
	GroupName = "gateway.kubesphere.io"
	Version   = "v1alpha2"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

func AddToContainer(container *restful.Container, cache cache.Cache) error {
	handler := newHandler(cache)
	ws := runtime.NewWebService(GroupVersion)

	ws.Route(ws.GET("/namespaces/{namespace}/availableingressclassscopes").
		To(handler.ingressClassScopeList).
		Doc("List ingressClassScope available for the namespace").
		Returns(http.StatusOK, api.StatusOK, []gatewayv1alpha2.IngressClassScope{}))

	container.Add(ws)
	return nil
}
