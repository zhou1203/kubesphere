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
	"kubesphere.io/kubesphere/pkg/server/errors"
)

var GroupVersion = schema.GroupVersion{Group: "iam.kubesphere.io", Version: "v1beta1"}

func AddToContainer(container *restful.Container, im im.IdentityManagementInterface, am am.AccessManagementInterface) error {
	ws := apiserverruntime.NewWebService(GroupVersion)
	handler := newIAMHandler(im, am)

	// users
	ws.Route(ws.POST("/users").
		To(handler.CreateUser).
		Doc("Create a global user account.").
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.User{}).
		Reads(iamv1beta1.User{}))
	ws.Route(ws.PUT("/users/{user}").
		To(handler.UpdateUser).
		Doc("Update user profile.").
		Reads(iamv1beta1.User{}).
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.User{}))
	ws.Route(ws.DELETE("/users/{user}").
		To(handler.DeleteUser).
		Doc("Delete the specified user.").
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))
	ws.Route(ws.PUT("/users/{user}/password").
		To(handler.ModifyPassword).
		Doc("Reset password of the specified user.").
		Reads(PasswordReset{}).
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))
	ws.Route(ws.GET("/users/{user}").
		To(handler.DescribeUser).
		Doc("Retrieve user details.").
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.User{}))
	ws.Route(ws.GET("/users").
		To(handler.ListUsers).
		Param(ws.QueryParameter("globalrole", "specific golalrole name")).
		Doc("List all users.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))
	ws.Route(ws.GET("/users/{user}/loginrecords").
		To(handler.ListUserLoginRecords).
		Param(ws.PathParameter("user", "username of the user")).
		Doc("List login records of the specified user.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.LoginRecord{}}}))

	// members
	ws.Route(ws.GET("/clustermembers").
		To(handler.ListClusterMembers).
		Param(ws.QueryParameter("clusterrole", "specific the cluster role name")).
		Doc("List all members in cluster").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))
	ws.Route(ws.POST("/clustermembers").
		To(handler.CreateClusterMembers).
		Doc("Add members to current cluster in bulk.").
		Reads([]Member{}).
		Returns(http.StatusOK, api.StatusOK, []Member{}))
	ws.Route(ws.DELETE("/clustermembers/{clustermember}").
		To(handler.RemoveClusterMember).
		Doc("Delete a member from current cluster.").
		Param(ws.PathParameter("clustermember", "cluster member's username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.GET("/workspaces/{workspace}/workspacemembers").
		To(handler.ListWorkspaceMembers).
		Param(ws.QueryParameter("workspacerole", "specific the workspace role name")).
		Doc("List all members in the specified workspace.").
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))
	ws.Route(ws.POST("/workspaces/{workspace}/workspacemembers").
		To(handler.CreateWorkspaceMembers).
		Doc("Add members to current cluster in bulk.").
		Param(ws.PathParameter("workspace", "workspace name")).
		Reads([]Member{}).
		Returns(http.StatusOK, api.StatusOK, []Member{}))
	ws.Route(ws.DELETE("/workspaces/{workspace}/workspacemembers/{workspacemember}").
		To(handler.RemoveWorkspaceMember).
		Doc("Delete a member from the workspace.").
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("workspacemember", "workspace member's username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.GET("/namespaces/{namespace}/namespacemembers").
		To(handler.ListNamespaceMembers).
		Param(ws.QueryParameter("role", "specific the role name")).
		Doc("List all members in the specified namespace.").
		Param(ws.PathParameter("namespace", "namespace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))
	ws.Route(ws.POST("/namespaces/{namespace}/namespacemembers").
		To(handler.CreateNamespaceMembers).
		Doc("Add members to the namespace in bulk.").
		Reads([]Member{}).
		Returns(http.StatusOK, api.StatusOK, []Member{}).
		Param(ws.PathParameter("namespace", "namespace")))
	ws.Route(ws.DELETE("/namespaces/{namespace}/namespacemembers/{member}").
		To(handler.RemoveNamespaceMember).
		Doc("Delete a member from the namespace.").
		Param(ws.PathParameter("namespace", "namespace")).
		Param(ws.PathParameter("member", "namespace member's username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.GET("/users/{username}/roletemplates").
		To(handler.ListRoleTemplateOfUser).
		Doc("List all role templates of the specified user").
		Param(ws.PathParameter("username", "the name of the specified user")).
		Param(ws.QueryParameter("scope", "the scope of role templates")).
		Param(ws.QueryParameter("workspace", "specific the workspace of the user at, only used when the scope is workspace")).
		Param(ws.QueryParameter("namespace", "specific the namespace of the user at, only used when the scope is namespace")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.RoleTemplate{}}}))

	ws.Route(ws.POST("/subjectaccessreviews").
		To(handler.CreateSubjectAccessReview).
		Doc("Evaluates all of the request attributes against all policies and allows or denies the request.").
		Reads(iamv1beta1.SubjectAccessReview{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.SubjectAccessReview{}))

	container.Add(ws)
	return nil
}
