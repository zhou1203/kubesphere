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

package v1alpha3

import (
	"encoding/json"
	"fmt"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/models/tenant"
	servererr "kubesphere.io/kubesphere/pkg/server/errors"
)

type handler struct {
	tenant tenant.Interface
}

func (h *handler) ListWorkspaces(req *restful.Request, resp *restful.Response) {
	queryParam := query.ParseQueryParameter(req)
	user, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, nil, err)
		return
	}

	result, err := h.tenant.ListWorkspaces(user, queryParam)
	if err != nil {
		api.HandleInternalError(resp, nil, err)
		return
	}

	resp.WriteEntity(result)
}

func (h *handler) GetWorkspace(request *restful.Request, response *restful.Response) {
	workspace, err := h.tenant.GetWorkspace(request.PathParameter("workspace"))
	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(workspace)
}

func (h *handler) CreateWorkspaceTemplate(req *restful.Request, resp *restful.Response) {
	var workspace tenantv1alpha2.WorkspaceTemplate

	err := req.ReadEntity(&workspace)

	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(resp, req, err)
		return
	}
	requestUser, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, req, err)
	}

	created, err := h.tenant.CreateWorkspaceTemplate(requestUser, &workspace)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(resp, req, err)
			return
		}
		if errors.IsForbidden(err) {
			api.HandleForbidden(resp, req, err)
			return
		}
		api.HandleBadRequest(resp, req, err)
		return
	}

	resp.WriteEntity(created)
}

func (h *handler) DeleteWorkspaceTemplate(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")

	opts := metav1.DeleteOptions{}

	err := request.ReadEntity(&opts)
	if err != nil {
		opts = *metav1.NewDeleteOptions(0)
	}

	err = h.tenant.DeleteWorkspaceTemplate(workspace, opts)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleBadRequest(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) UpdateWorkspaceTemplate(req *restful.Request, resp *restful.Response) {
	workspaceName := req.PathParameter("workspace")
	var workspace tenantv1alpha2.WorkspaceTemplate

	err := req.ReadEntity(&workspace)

	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(resp, req, err)
		return
	}

	if workspaceName != workspace.Name {
		err := fmt.Errorf("the name of the object (%s) does not match the name on the URL (%s)", workspace.Name, workspaceName)
		klog.Errorf("%+v", err)
		api.HandleBadRequest(resp, req, err)
		return
	}

	requestUser, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, req, err)
	}

	updated, err := h.tenant.UpdateWorkspaceTemplate(requestUser, &workspace)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(resp, req, err)
			return
		}
		if errors.IsBadRequest(err) {
			api.HandleBadRequest(resp, req, err)
			return
		}
		if errors.IsForbidden(err) {
			api.HandleForbidden(resp, req, err)
			return
		}
		api.HandleInternalError(resp, req, err)
		return
	}

	resp.WriteEntity(updated)
}

func (h *handler) DescribeWorkspaceTemplate(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")
	workspace, err := h.tenant.DescribeWorkspaceTemplate(workspaceName)
	if err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}
	response.WriteEntity(workspace)
}

func (h *handler) PatchWorkspaceTemplate(req *restful.Request, resp *restful.Response) {
	workspaceName := req.PathParameter("workspace")
	var data json.RawMessage
	err := req.ReadEntity(&data)
	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(resp, req, err)
		return
	}

	requestUser, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, req, err)
	}

	patched, err := h.tenant.PatchWorkspaceTemplate(requestUser, workspaceName, data)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(resp, req, err)
			return
		}
		if errors.IsBadRequest(err) {
			api.HandleBadRequest(resp, req, err)
			return
		}
		if errors.IsNotFound(err) {
			api.HandleForbidden(resp, req, err)
			return
		}
		api.HandleInternalError(resp, req, err)
		return
	}

	resp.WriteEntity(patched)
}

func (h *handler) ListWorkspaceTemplates(req *restful.Request, resp *restful.Response) {
	user, ok := request.UserFrom(req.Request.Context())
	queryParam := query.ParseQueryParameter(req)

	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, nil, err)
		return
	}

	result, err := h.tenant.ListWorkspaceTemplates(user, queryParam)

	if err != nil {
		api.HandleInternalError(resp, nil, err)
		return
	}

	resp.WriteEntity(result)
}
