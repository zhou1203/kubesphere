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

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	auditingclient "kubesphere.io/kubesphere/pkg/simple/client/auditing"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	quotav1alpha2 "kubesphere.io/api/quota/v1alpha2"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	auditingv1alpha1 "kubesphere.io/kubesphere/pkg/api/auditing/v1alpha1"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

const (
	GroupName = "tenant.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func AddToContainer(c *restful.Container, cacheClient runtimeclient.Client, auditingClient auditingclient.Client,
	clusterClient clusterclient.Interface, am am.AccessManagementInterface, im im.IdentityManagementInterface,
	authorizer authorizer.Authorizer) error {
	mimePatch := []string{restful.MIME_JSON, runtime.MimeMergePatchJson, runtime.MimeJsonPatchJson}

	ws := runtime.NewWebService(GroupVersion)
	handler := NewTenantHandler(cacheClient, auditingClient, clusterClient, am, im, authorizer)

	ws.Route(ws.GET("/clusters").
		To(handler.ListClusters).
		Doc("List clusters available to users").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.UserResourceTag}))

	ws.Route(ws.POST("/workspaces").
		To(handler.CreateWorkspaceTemplate).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}).
		Doc("Create workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.DELETE("/workspaces/{workspace}").
		To(handler.DeleteWorkspaceTemplate).
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, errors.None).
		Doc("Delete workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.PUT("/workspaces/{workspace}").
		To(handler.UpdateWorkspaceTemplate).
		Param(ws.PathParameter("workspace", "workspace name")).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}).
		Doc("Update workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.PATCH("/workspaces/{workspace}").
		To(handler.PatchWorkspaceTemplate).
		Param(ws.PathParameter("workspace", "workspace name")).
		Consumes(mimePatch...).
		Reads(tenantv1alpha2.WorkspaceTemplate{}).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}).
		Doc("Update workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.GET("/workspaces").
		To(handler.ListWorkspaceTemplates).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}).
		Doc("List all workspaces that belongs to the current user").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}").
		To(handler.DescribeWorkspaceTemplate).
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, tenantv1alpha2.WorkspaceTemplate{}).
		Doc("Describe workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/clusters").
		To(handler.ListWorkspaceClusters).
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}).
		Doc("List clusters authorized to the specified workspace.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.GET("/namespaces").
		To(handler.ListNamespaces).
		Doc("List the namespaces for the current user").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/namespaces").
		To(handler.ListNamespaces).
		Param(ws.PathParameter("workspace", "workspace name")).
		Doc("List the namespaces of the specified workspace for the current user").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/namespaces/{namespace}").
		To(handler.DescribeNamespace).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("namespace", "project name")).
		Doc("Retrieve namespace details.").
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/namespaces/{namespace}").
		To(handler.DeleteNamespace).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("namespace", "project name")).
		Doc("Delete namespace.").
		Returns(http.StatusOK, api.StatusOK, errors.None).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.POST("/workspaces/{workspace}/namespaces").
		To(handler.CreateNamespace).
		Param(ws.PathParameter("workspace", "workspace name")).
		Doc("Create the namespaces of the specified workspace for the current user").
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/workspacemembers/{workspacemember}/namespaces").
		To(handler.ListNamespaces).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("workspacemember", "workspacemember username")).
		Doc("List the namespaces of the specified workspace for the workspace member").
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.PUT("/workspaces/{workspace}/namespaces/{namespace}").
		To(handler.UpdateNamespace).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("namespace", "project name")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.PATCH("/workspaces/{workspace}/namespaces/{namespace}").
		To(handler.PatchNamespace).
		Consumes(mimePatch...).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("namespace", "project name")).
		Reads(corev1.Namespace{}).
		Returns(http.StatusOK, api.StatusOK, corev1.Namespace{}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.NamespaceTag}))

	ws.Route(ws.GET("/auditing/events").
		To(handler.Auditing).
		Doc("Query auditing events against the cluster").
		Param(ws.QueryParameter("operation", "Operation type. This can be one of three types: `query` (for querying events), `statistics` (for retrieving statistical data), `histogram` (for displaying events count by time interval). Defaults to query.").DefaultValue("query")).
		Param(ws.QueryParameter("workspace_filter", "A comma-separated list of workspaces. This field restricts the query to specified workspaces. For example, the following filter matches the workspace my-ws and demo-ws: `my-ws,demo-ws`.")).
		Param(ws.QueryParameter("workspace_search", "A comma-separated list of keywords. Differing from **workspace_filter**, this field performs fuzzy matching on workspaces. For example, the following value limits the query to workspaces whose name contains the word my(My,MY,...) *OR* demo(Demo,DemO,...): `my,demo`.")).
		Param(ws.QueryParameter("objectref_namespace_filter", "A comma-separated list of namespaces. This field restricts the query to specified `ObjectRef.Namespace`.")).
		Param(ws.QueryParameter("objectref_namespace_search", "A comma-separated list of keywords. Differing from **objectref_namespace_filter**, this field performs fuzzy matching on `ObjectRef.Namespace`.")).
		Param(ws.QueryParameter("objectref_name_filter", "A comma-separated list of names. This field restricts the query to specified `ObjectRef.Name`.")).
		Param(ws.QueryParameter("objectref_name_search", "A comma-separated list of keywords. Differing from **objectref_name_filter**, this field performs fuzzy matching on `ObjectRef.Name`.")).
		Param(ws.QueryParameter("level_filter", "A comma-separated list of levels. This know values are Metadata, Request, RequestResponse.")).
		Param(ws.QueryParameter("verb_filter", "A comma-separated list of verbs. This field restricts the query to specified verb. This field restricts the query to specified `Verb`.")).
		Param(ws.QueryParameter("user_filter", "A comma-separated list of user. This field restricts the query to specified user. For example, the following filter matches the user user1 and user2: `user1,user2`.")).
		Param(ws.QueryParameter("user_search", "A comma-separated list of keywords. Differing from **user_filter**, this field performs fuzzy matching on 'User.username'. For example, the following value limits the query to user whose name contains the word my(My,MY,...) *OR* demo(Demo,DemO,...): `my,demo`.")).
		Param(ws.QueryParameter("group_search", "A comma-separated list of keywords. This field performs fuzzy matching on 'User.Groups'. For example, the following value limits the query to group which contains the word my(My,MY,...) *OR* demo(Demo,DemO,...): `my,demo`.")).
		Param(ws.QueryParameter("source_ip_search", "A comma-separated list of keywords. This field performs fuzzy matching on 'SourceIPs'. For example, the following value limits the query to SourceIPs which contains 127.0 *OR* 192.168.: `127.0,192.168.`.")).
		Param(ws.QueryParameter("objectref_resource_filter", "A comma-separated list of resource. This field restricts the query to specified ip. This field restricts the query to specified `ObjectRef.Resource`.")).
		Param(ws.QueryParameter("objectref_subresource_filter", "A comma-separated list of subresource. This field restricts the query to specified subresource. This field restricts the query to specified `ObjectRef.Subresource`.")).
		Param(ws.QueryParameter("response_code_filter", "A comma-separated list of response status code. This field restricts the query to specified response status code. This field restricts the query to specified `ResponseStatus.code`.")).
		Param(ws.QueryParameter("response_status_filter", "A comma-separated list of response status. This field restricts the query to specified response status. This field restricts the query to specified `ResponseStatus.status`.")).
		Param(ws.QueryParameter("start_time", "Start time of query (limits `RequestReceivedTimestamp`). The format is a string representing seconds since the epoch, eg. 1136214245.")).
		Param(ws.QueryParameter("end_time", "End time of query (limits `RequestReceivedTimestamp`). The format is a string representing seconds since the epoch, eg. 1136214245.")).
		Param(ws.QueryParameter("interval", "Time interval. It requires **operation** is set to `histogram`. The format is [0-9]+[smhdwMqy]. Defaults to 15m (i.e. 15 min).").DefaultValue("15m")).
		Param(ws.QueryParameter("sort", "Sort order. One of asc, desc. This field sorts events by `RequestReceivedTimestamp`.").DataType("string").DefaultValue("desc")).
		Param(ws.QueryParameter("from", "The offset from the result set. This field returns query results from the specified offset. It requires **operation** is set to `query`. Defaults to 0 (i.e. from the beginning of the result set).").DataType("integer").DefaultValue("0").Required(false)).
		Param(ws.QueryParameter("size", "Size of result set to return. It requires **operation** is set to `query`. Defaults to 10 (i.e. 10 event records).").DataType("integer").DefaultValue("10").Required(false)).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.AuditingQueryTag}).
		Writes(auditingv1alpha1.APIResponse{}).
		Returns(http.StatusOK, api.StatusOK, auditingv1alpha1.APIResponse{}))

	ws.Route(ws.POST("/workspaces/{workspace}/resourcequotas").
		To(handler.CreateWorkspaceResourceQuota).
		Doc("Create resource quota.").
		Reads(quotav1alpha2.ResourceQuota{}).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(handler.DeleteWorkspaceResourceQuota).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("resourcequota", "resource quota name")).
		Returns(http.StatusOK, api.StatusOK, errors.None).
		Doc("Delete resource quota.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.PUT("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(handler.UpdateWorkspaceResourceQuota).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("resourcequota", "resource quota name")).
		Reads(quotav1alpha2.ResourceQuota{}).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}).
		Doc("Update resource quota.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/resourcequotas/{resourcequota}").
		To(handler.DescribeWorkspaceResourceQuota).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("resourcequota", "resource quota name")).
		Returns(http.StatusOK, api.StatusOK, quotav1alpha2.ResourceQuota{}).
		Doc("Describe resource quota.").
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.WorkspaceTag}))

	c.Add(ws)
	return nil
}
