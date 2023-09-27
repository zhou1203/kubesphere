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
	"net/http"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	quotav1alpha2 "kubesphere.io/api/quota/v1alpha2"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/models/tenant"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/simple/client/overview"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const (
	GroupName = "tenant.kubesphere.io"
)

var mimePatch = []string{restful.MIME_JSON, runtime.MimeMergePatchJson, runtime.MimeJsonPatchJson}

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func NewHandler(client runtimeclient.Client, clusterClient clusterclient.Interface, am am.AccessManagementInterface,
	im im.IdentityManagementInterface, authorizer authorizer.Authorizer, counter overview.Counter) rest.Handler {
	return &handler{
		tenant:  tenant.New(client, clusterClient, am, im, authorizer),
		client:  client,
		auth:    authorizer,
		counter: counter,
	}
}

func NewFakeHandler() rest.Handler {
	return &handler{}
}

func (h *handler) AddToContainer(c *restful.Container) error {
	ws := runtime.NewWebService(GroupVersion)

	ws.Route(ws.GET("/clusters").
		To(h.ListClusters).
		Doc("List clusters available to users").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Operation("user-related-clusters").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.POST("/workspaces").
		To(h.CreateWorkspaceTemplate).
		Doc("Create workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Operation("create-workspace").
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}").
		To(h.DeleteWorkspaceTemplate).
		Doc("Delete workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Operation("delete-workspace").
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.PUT("/workspaces/{workspace}").
		To(h.UpdateWorkspaceTemplate).
		Doc("Update workspace").
		Operation("update-workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.PATCH("/workspaces/{workspace}").
		To(h.PatchWorkspaceTemplate).
		Consumes(mimePatch...).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Doc("Update workspace").
		Operation("patch-workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.GET("/workspaces").
		To(h.ListWorkspaceTemplates).
		Doc("List workspaces").
		Operation("list-workspaces").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	ws.Route(ws.GET("/workspaces/{workspace}").
		To(h.DescribeWorkspaceTemplate).
		Doc("Get workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Operation("get-workspace").
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.GET("/workspaces/{workspace}/clusters").
		To(h.ListWorkspaceClusters).
		Doc("List clusters authorized to the specified workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.GET("/namespaces").
		To(h.ListNamespaces).
		Doc("List the namespaces for the current user").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Operation("list-namespaces").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.GET("/workspaces/{workspace}/namespaces").
		To(h.ListNamespaces).
		Doc("List the namespaces in workspace").
		Operation("list-namespaces-workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.GET("/workspaces/{workspace}/namespaces/{namespace}").
		To(h.DescribeNamespace).
		Doc("Get namespace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("namespace", "The specified namespace.")).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/namespaces/{namespace}").
		To(h.DeleteNamespace).
		Doc("Delete namespace from workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("namespace", "The specified namespace.")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.POST("/workspaces/{workspace}/namespaces").
		To(h.CreateNamespace).
		Doc("Create namespace in workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}))

	ws.Route(ws.GET("/workspaces/{workspace}/workspacemembers/{workspacemember}/namespaces").
		To(h.ListNamespaces).
		Doc("List namespaces in workspace of the member").
		Operation("list-namespaces-workspace-member").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("workspacemember", "workspacemember username")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}))

	ws.Route(ws.PUT("/workspaces/{workspace}/namespaces/{namespace}").
		To(h.UpdateNamespace).
		Doc("Update namespace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Notes("Update namespace").
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("namespace", "The specified namespace.")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}))

	ws.Route(ws.PATCH("/workspaces/{workspace}/namespaces/{namespace}").
		To(h.PatchNamespace).
		Consumes(mimePatch...).
		Doc("Patch namespace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Notes("Patch the specified namespace in workspace.").
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("namespace", "The specified namespace.")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}))

	ws.Route(ws.POST("/workspaces/{workspace}/resourcequotas").
		To(h.CreateWorkspaceResourceQuota).
		Doc("Create workspace resource quota").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Reads(quotav1alpha2.ResourceQuota{}).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(h.DeleteWorkspaceResourceQuota).
		Doc("Delete workspace resource quota.").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("resourcequota", "resource quota name")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.PUT("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(h.UpdateWorkspaceResourceQuota).
		Doc("Update workspace resource quota").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("resourcequota", "Resource quota name")).
		Reads(quotav1alpha2.ResourceQuota{}).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}))

	ws.Route(ws.GET("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(h.DescribeWorkspaceResourceQuota).
		Doc("Get workspace resource quota").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Param(ws.PathParameter("resourcequota", "Resource quota name")).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}))

	ws.Route(ws.GET("/workspaces/{workspace}/metrics").
		To(h.GetWorkspaceMetrics).
		Doc("Get workspace metrics").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, overview.MetricResults{}))

	ws.Route(ws.GET("/metrics").
		To(h.GetPlatformMetrics).
		Doc("Get platform metrics").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Returns(http.StatusOK, api.StatusOK, overview.MetricResults{}))

	c.Add(ws)
	return nil
}
