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

package v1alpha2

import (
	"errors"
	"net/http"

	"github.com/emicklei/go-restful/v3"
	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	requestctx "kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/models/terminal"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow connections from any Origin
	CheckOrigin: func(r *http.Request) bool { return true },
}

type handler struct {
	terminaler terminal.Interface
	authorizer authorizer.Authorizer
}

func (h *handler) HandleTerminalSession(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	podName := request.PathParameter("pod")
	containerName := request.QueryParameter("container")
	shell := request.QueryParameter("shell")

	user, _ := requestctx.UserFrom(request.Request.Context())

	createPodsExec := authorizer.AttributesRecord{
		User:            user,
		Verb:            "create",
		Resource:        "pods",
		Subresource:     "exec",
		Namespace:       namespace,
		ResourceRequest: true,
		ResourceScope:   requestctx.NamespaceScope,
	}

	decision, reason, err := h.authorizer.Authorize(createPodsExec)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	if decision != authorizer.DecisionAllow {
		api.HandleForbidden(response, request, errors.New(reason))
		return
	}

	conn, err := upgrader.Upgrade(response.ResponseWriter, request.Request, nil)
	if err != nil {
		klog.Warning(err)
		return
	}

	h.terminaler.HandleSession(shell, namespace, podName, containerName, conn)
}

func (h *handler) HandleShellAccessToNode(request *restful.Request, response *restful.Response) {
	nodename := request.PathParameter("nodename")

	user, _ := requestctx.UserFrom(request.Request.Context())

	createNodesExec := authorizer.AttributesRecord{
		User:            user,
		Verb:            "create",
		Resource:        "nodes",
		Subresource:     "exec",
		ResourceRequest: true,
		ResourceScope:   requestctx.ClusterScope,
	}

	decision, reason, err := h.authorizer.Authorize(createNodesExec)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	if decision != authorizer.DecisionAllow {
		api.HandleForbidden(response, request, errors.New(reason))
		return
	}

	conn, err := upgrader.Upgrade(response.ResponseWriter, request.Request, nil)
	if err != nil {
		klog.Warning(err)
		return
	}

	h.terminaler.HandleShellAccessToNode(nodename, conn)
}
