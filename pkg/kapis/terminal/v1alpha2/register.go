/*
Copyright 2019 The KubeSphere Authors.

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
	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	restapi "kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models"
	"kubesphere.io/kubesphere/pkg/models/terminal"
)

const (
	GroupName = "terminal.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

func NewHandler(client kubernetes.Interface, authorizer authorizer.Authorizer, config *rest.Config, options *terminal.Options) restapi.Handler {
	return &handler{
		authorizer: authorizer,
		terminaler: terminal.NewTerminaler(client, config, options),
	}
}

func NewFakeHandler() restapi.Handler {
	return &handler{}
}

func (h *handler) AddToContainer(c *restful.Container) error {
	ws := runtime.NewWebService(GroupVersion)

	ws.Route(ws.GET("/namespaces/{namespace}/pods/{pod}/exec").
		To(h.HandleTerminalSession).
		Doc("Create pod terminal session").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagTerminal}).
		Operation("create-pod-exec").
		Param(ws.PathParameter("namespace", "The specified namespace.")).
		Param(ws.PathParameter("pod", "pod name")).
		Writes(models.PodInfo{}))

	//Add new Route to support shell access to the node
	ws.Route(ws.GET("/nodes/{nodename}/exec").
		To(h.HandleShellAccessToNode).
		Doc("Create node terminal session").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagTerminal}).
		Operation("create-node-exec").
		Param(ws.PathParameter("nodename", "node name")).
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagTerminal}).
		Writes(models.PodInfo{}))

	c.Add(ws)

	return nil
}
