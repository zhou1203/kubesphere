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

package v1alpha3

import (
	"net/http"

	"kubesphere.io/kubesphere/pkg/server/errors"

	"kubesphere.io/kubesphere/pkg/models/tenant"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const (
	GroupName = "tenant.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha3"}

func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func NewHandler(client runtimeclient.Client, clusterClient clusterclient.Interface, am am.AccessManagementInterface,
	im im.IdentityManagementInterface, authorizer authorizer.Authorizer) rest.Handler {
	return &handler{
		tenant: tenant.New(client, clusterClient, am, im, authorizer),
	}
}

func NewFakeHandler() rest.Handler {
	return &handler{}
}

func (h *handler) AddToContainer(c *restful.Container) error {
	mimePatch := []string{restful.MIME_JSON, runtime.MimeMergePatchJson, runtime.MimeJsonPatchJson}
	ws := runtime.NewWebService(GroupVersion)

	ws.Route(ws.POST("/workspacetemplates").
		To(h.CreateWorkspaceTemplate).
		Doc("Create workspace template").
		Operation("create-workspace-template").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.DELETE("/workspacetemplates/{workspace}").
		To(h.DeleteWorkspaceTemplate).
		Doc("Delete workspace template").
		Operation("delete-workspace-template").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.PUT("/workspacetemplates/{workspace}").
		To(h.UpdateWorkspaceTemplate).
		Doc("Update workspace template").
		Operation("update-workspace-template").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.PATCH("/workspacetemplates/{workspace}").
		To(h.PatchWorkspaceTemplate).
		Consumes(mimePatch...).
		Doc("Patch workspace template").
		Operation("patch-workspace-template").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.GET("/workspacetemplates").
		To(h.ListWorkspaceTemplates).
		Doc("List all workspace templates").
		Operation("list-workspace-templates").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	ws.Route(ws.GET("/workspacetemplates/{workspace}").
		To(h.DescribeWorkspaceTemplate).
		Doc("Get workspace template").
		Operation("get-workspace-template").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}))

	ws.Route(ws.GET("/workspaces").
		To(h.ListWorkspaces).
		Doc("List all workspaces").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	ws.Route(ws.GET("/workspaces/{workspace}").
		To(h.GetWorkspace).
		Doc("Get workspace").
		Metadata(restfulspec.KeyOpenAPITags, []string{api.TagUserRelatedResources}).
		Param(ws.PathParameter("workspace", "The specified workspace.")).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha1.Workspace{}))

	c.Add(ws)
	return nil
}
