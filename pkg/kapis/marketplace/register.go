/*
Copyright 2020 The KubeSphere Authors.

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

package marketplace

import (
	"github.com/emicklei/go-restful/v3"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func AddToContainer(c *restful.Container, client runtimeclient.Client) error {
	ws := &restful.WebService{}
	ws.Path("/marketplace").
		Produces(restful.MIME_JSON)

	handler := &handler{client: client}

	ws.Route(ws.POST("/bind").To(handler.bind))
	ws.Route(ws.GET("/callback").To(handler.callback))
	ws.Route(ws.POST("/sync").To(handler.sync))
	ws.Route(ws.POST("/unbind").To(handler.unbind))

	c.Add(ws)
	return nil
}
