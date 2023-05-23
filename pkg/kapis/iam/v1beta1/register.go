package v1beta1

import (
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/api"
	apiserverruntime "kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
)

var GroupVersion = schema.GroupVersion{Group: "iam.kubesphere.io", Version: "v1beta1"}

func AddToContainer(container *restful.Container, im im.IdentityManagementInterface, am am.AccessManagementInterface) error {
	ws := apiserverruntime.NewWebService(GroupVersion)
	handler := newIAMHandler(im, am)

	ws.Route(ws.GET("/users/{username}/roletemplates").
		To(handler.ListRoleTemplateOfUser).
		Doc("List all role templates of the specified user").
		Param(ws.PathParameter("username", "the name of the specified user")).
		Param(ws.QueryParameter("scope", "the scope of role templates")).
		Param(ws.QueryParameter("workspace", "specific the workspace of the user at, only used when the scope is workspace")).
		Param(ws.QueryParameter("namespace", "specific the namespace of the user at, only used when the scope is namespace")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.RoleTemplate{}}}))

	ws.Route(ws.GET("/clustermembers").
		To(handler.ListClusterMembers).
		Doc("List all members in cluster").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))

	ws.Route(ws.GET("/workspaces/{workspace}/workspacemembers").
		To(handler.ListWorkspaceMembers).
		Doc("List all members in the specified workspace.").
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))

	ws.Route(ws.GET("/namespace/{namespace}/namespacemembers").
		To(handler.ListWorkspaceMembers).
		Doc("List all members in the specified namespace.").
		Param(ws.PathParameter("namespace", "namespace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))

	ws.Route(ws.GET("/users/{username}/roles").
		To(handler.GetRoleOfUser).
		Doc("Get the user`s role").
		Param(ws.QueryParameter("namespace", "Specific the namespace of the user at")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.Role{}}}))

	ws.Route(ws.GET("/users/{username}/workspaceroles").
		To(handler.GetWorkspaceRoleOfUser).
		Doc("Get the user`s workspace role").
		Param(ws.QueryParameter("workspace", "Specific the workspace of the user at")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.WorkspaceRole{}}}))

	ws.Route(ws.GET("/users/{username}/clusterroles").
		To(handler.GetClusterRoleOfUser).
		Doc("Get the user`s workspace role").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.ClusterRole{}}}))

	ws.Route(ws.GET("/users/{username}/globalroles").
		To(handler.GetGlobalRoleOfUser).
		Doc("Get the user`s global role").
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.GlobalRole{}))

	ws.Route(ws.POST("/subjectaccessreviews").
		To(handler.CreateSubjectAccessReview).
		Doc("Evaluates all of the request attributes against all policies and allows or denies the request.").
		Reads(iamv1beta1.SubjectAccessReview{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.SubjectAccessReview{}))

	container.Add(ws)
	return nil
}
