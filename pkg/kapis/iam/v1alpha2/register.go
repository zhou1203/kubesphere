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

	"github.com/emicklei/go-restful/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	apiserverruntime "kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

const (
	GroupName = "iam.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

func AddToContainer(container *restful.Container, im im.LegacyIdentityManagementInterface, am am.LegacyAccessManagementInterface, authorizer authorizer.Authorizer) error {
	ws := apiserverruntime.NewWebService(GroupVersion)
	handler := newIAMHandler(im, am, authorizer)

	// users
	ws.Route(ws.POST("/users").
		To(handler.CreateUser).
		Doc("Create a global user account.").
		Returns(http.StatusOK, api.StatusOK, iamv1alpha2.User{}).
		Reads(iamv1alpha2.User{}))
	ws.Route(ws.DELETE("/users/{user}").
		To(handler.DeleteUser).
		Doc("Delete the specified user.").
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, errors.None))
	ws.Route(ws.PUT("/users/{user}").
		To(handler.UpdateUser).
		Doc("Update user profile.").
		Reads(iamv1alpha2.User{}).
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, iamv1alpha2.User{}))
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
		Returns(http.StatusOK, api.StatusOK, iamv1alpha2.User{}))
	ws.Route(ws.GET("/users").
		To(handler.ListUsers).
		Doc("List all users.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1alpha2.User{}}}))

	ws.Route(ws.GET("/users/{user}/globalroles").
		To(handler.RetrieveMemberRoleTemplates).
		Doc("Retrieve user's global role templates.").
		Param(ws.PathParameter("user", "username")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1alpha2.GlobalRole{}}}))
	ws.Route(ws.GET("/clustermembers/{clustermember}/clusterroles").
		To(handler.RetrieveMemberRoleTemplates).
		Doc("Retrieve user's role templates in cluster.").
		Param(ws.PathParameter("clustermember", "cluster member's username")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&rbacv1.ClusterRole{}}}))
	ws.Route(ws.GET("/workspaces/{workspace}/workspacemembers/{workspacemember}/workspaceroles").
		To(handler.RetrieveMemberRoleTemplates).
		Doc("Retrieve member's role templates in workspace.").
		Param(ws.PathParameter("workspacemember", "workspace member's username")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1alpha2.WorkspaceRole{}}}))
	ws.Route(ws.GET("/namespaces/{namespace}/members/{member}/roles").
		To(handler.RetrieveMemberRoleTemplates).
		Doc("Retrieve member's role templates in namespace.").
		Param(ws.PathParameter("member", "namespace member's username")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&rbacv1.Role{}}}))

	container.Add(ws)
	return nil
}
