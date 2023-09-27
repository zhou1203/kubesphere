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
	"encoding/json"
	"fmt"

	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	quotav1alpha2 "kubesphere.io/api/quota/v1alpha2"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/models/tenant"
	servererr "kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/overview"
)

type handler struct {
	tenant  tenant.Interface
	auth    authorizer.Authorizer
	counter overview.Counter
	client  runtimeclient.Client
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

func (h *handler) ListNamespaces(req *restful.Request, resp *restful.Response) {
	workspace := req.PathParameter("workspace")
	queryParam := query.ParseQueryParameter(req)

	var workspaceMember user.Info
	if username := req.PathParameter("workspacemember"); username != "" {
		workspaceMember = &user.DefaultInfo{
			Name: username,
		}
	} else {
		requestUser, ok := request.UserFrom(req.Request.Context())
		if !ok {
			err := fmt.Errorf("cannot obtain user info")
			klog.Errorln(err)
			api.HandleForbidden(resp, nil, err)
			return
		}
		workspaceMember = requestUser
	}

	result, err := h.tenant.ListNamespaces(workspaceMember, workspace, queryParam)
	if err != nil {
		api.HandleInternalError(resp, nil, err)
		return
	}

	resp.WriteEntity(result)
}

func (h *handler) CreateNamespace(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	var namespace corev1.Namespace

	err := request.ReadEntity(&namespace)

	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(response, request, err)
		return
	}

	created, err := h.tenant.CreateNamespace(workspace, &namespace)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleBadRequest(response, request, err)
		return
	}

	response.WriteEntity(created)
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

func (h *handler) ListWorkspaceClusters(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")

	result, err := h.tenant.ListWorkspaceClusters(workspaceName)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(result)
}

func (h *handler) DescribeNamespace(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")
	namespaceName := request.PathParameter("namespace")
	ns, err := h.tenant.DescribeNamespace(workspaceName, namespaceName)

	if err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(ns)
}

func (h *handler) DeleteNamespace(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")
	namespaceName := request.PathParameter("namespace")

	err := h.tenant.DeleteNamespace(workspaceName, namespaceName)

	if err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) UpdateNamespace(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")
	namespaceName := request.PathParameter("namespace")

	var namespace corev1.Namespace
	err := request.ReadEntity(&namespace)
	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(response, request, err)
		return
	}

	if namespaceName != namespace.Name {
		err := fmt.Errorf("the name of the object (%s) does not match the name on the URL (%s)", namespace.Name, namespaceName)
		klog.Errorf("%+v", err)
		api.HandleBadRequest(response, request, err)
		return
	}

	updated, err := h.tenant.UpdateNamespace(workspaceName, &namespace)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		if errors.IsBadRequest(err) {
			api.HandleBadRequest(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(updated)
}

func (h *handler) PatchNamespace(request *restful.Request, response *restful.Response) {
	workspaceName := request.PathParameter("workspace")
	namespaceName := request.PathParameter("namespace")

	var namespace corev1.Namespace
	err := request.ReadEntity(&namespace)
	if err != nil {
		klog.Error(err)
		api.HandleBadRequest(response, request, err)
		return
	}

	namespace.Name = namespaceName

	patched, err := h.tenant.PatchNamespace(workspaceName, &namespace)

	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		if errors.IsBadRequest(err) {
			api.HandleBadRequest(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(patched)
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

func (h *handler) ListClusters(r *restful.Request, response *restful.Response) {
	user, ok := request.UserFrom(r.Request.Context())

	if !ok {
		response.WriteEntity([]interface{}{})
		return
	}

	queryParam := query.ParseQueryParameter(r)
	result, err := h.tenant.ListClusters(user, queryParam)
	if err != nil {
		klog.Error(err)
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, r, err)
			return
		}
		api.HandleInternalError(response, r, err)
		return
	}

	response.WriteEntity(result)
}

func (h *handler) CreateWorkspaceResourceQuota(r *restful.Request, response *restful.Response) {
	workspaceName := r.PathParameter("workspace")
	resourceQuota := &quotav1alpha2.ResourceQuota{}
	err := r.ReadEntity(resourceQuota)
	if err != nil {
		api.HandleBadRequest(response, r, err)
		return
	}
	result, err := h.tenant.CreateWorkspaceResourceQuota(workspaceName, resourceQuota)
	if err != nil {
		api.HandleInternalError(response, r, err)
		return
	}
	response.WriteEntity(result)
}

func (h *handler) DeleteWorkspaceResourceQuota(r *restful.Request, response *restful.Response) {
	workspace := r.PathParameter("workspace")
	resourceQuota := r.PathParameter("resourcequota")

	if err := h.tenant.DeleteWorkspaceResourceQuota(workspace, resourceQuota); err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, r, err)
			return
		}
		api.HandleInternalError(response, r, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) UpdateWorkspaceResourceQuota(r *restful.Request, response *restful.Response) {
	workspaceName := r.PathParameter("workspace")
	resourceQuotaName := r.PathParameter("resourcequota")
	resourceQuota := &quotav1alpha2.ResourceQuota{}
	err := r.ReadEntity(resourceQuota)
	if err != nil {
		api.HandleBadRequest(response, r, err)
		return
	}

	if resourceQuotaName != resourceQuota.Name {
		err := fmt.Errorf("the name of the object (%s) does not match the name on the URL (%s)", resourceQuota.Name, resourceQuotaName)
		klog.Errorf("%+v", err)
		api.HandleBadRequest(response, r, err)
		return
	}

	result, err := h.tenant.UpdateWorkspaceResourceQuota(workspaceName, resourceQuota)
	if err != nil {
		api.HandleInternalError(response, r, err)
		return
	}

	response.WriteEntity(result)
}

func (h *handler) DescribeWorkspaceResourceQuota(r *restful.Request, response *restful.Response) {
	workspaceName := r.PathParameter("workspace")
	resourceQuotaName := r.PathParameter("resourcequota")

	resourceQuota, err := h.tenant.DescribeWorkspaceResourceQuota(workspaceName, resourceQuotaName)
	if err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, r, err)
			return
		}
		api.HandleInternalError(response, r, err)
		return
	}

	response.WriteEntity(resourceQuota)
}

func (h *handler) GetWorkspaceMetrics(req *restful.Request, resp *restful.Response) {
	workspace := req.PathParameter("workspace")

	user, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, nil, err)
		return
	}
	parameter := query.ParseQueryParameter(req)
	prefix := "workspace"

	namespaces, err := h.tenant.ListNamespaces(user, workspace, parameter)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}

	metrics, err := h.counter.GetMetrics([]string{overview.WorkspaceRoleBindingCount, overview.WorkspaceRoleCount},
		"", workspace, prefix)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}

	metrics.AddMetric(overview.CustomMetric(overview.NamespaceCount, prefix, namespaces.TotalItems))

	_ = resp.WriteEntity(metrics)

}

func (h *handler) GetPlatformMetrics(req *restful.Request, resp *restful.Response) {
	user, ok := request.UserFrom(req.Request.Context())
	if !ok {
		err := fmt.Errorf("cannot obtain user info")
		klog.Errorln(err)
		api.HandleForbidden(resp, nil, err)
		return
	}
	parameter := query.ParseQueryParameter(req)
	prefix := "platform"

	metricNames := []string{overview.UserCount}
	metrics, err := h.counter.GetMetrics(metricNames, "", "", prefix)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}

	// check if the user has permission to visit extensions
	attr := authorizer.AttributesRecord{
		User:            user,
		Verb:            "list",
		APIGroup:        corev1alpha1.GroupName,
		APIVersion:      corev1alpha1.SchemeGroupVersion.Version,
		Resource:        "installplans",
		ResourceRequest: true,
		ResourceScope:   request.GlobalScope,
	}

	decision, _, err := h.auth.Authorize(attr)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}

	if decision == authorizer.DecisionAllow {
		// get installed extension count
		installPlanList := &corev1alpha1.InstallPlanList{}
		err = h.client.List(req.Request.Context(), installPlanList)
		if err != nil {
			api.HandleInternalError(resp, req, err)
			return
		}
		metrics.AddMetric(overview.CustomMetric(overview.InstallPlanCount, prefix, len(installPlanList.Items)))
	}

	// get count of workspaces a tenant can access
	workspaces, err := h.tenant.ListWorkspaceTemplates(user, parameter)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}
	if workspaces.TotalItems != 0 {
		metrics.AddMetric(overview.CustomMetric(overview.WorkspaceCount, prefix, workspaces.TotalItems))
	}

	// get count of clusters a tenant can access
	clusters, err := h.tenant.ListClusters(user, parameter)
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}
	if clusters.TotalItems != 0 {
		metrics.AddMetric(overview.CustomMetric(overview.ClusterCount, prefix, clusters.TotalItems))
	}

	_ = resp.WriteEntity(metrics)

}
