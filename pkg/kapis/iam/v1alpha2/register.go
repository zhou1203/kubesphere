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

	"k8s.io/apimachinery/pkg/runtime"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	apiserverruntime "kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/group"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

const (
	GroupName = "iam.kubesphere.io"
)

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

func AddToContainer(container *restful.Container, im im.IdentityManagementInterface, am am.AccessManagementInterface, group group.GroupOperator, authorizer authorizer.Authorizer) error {
	ws := apiserverruntime.NewWebService(GroupVersion)
	handler := newIAMHandler(im, am, group, authorizer)

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
		Doc("List all users.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.User{}}}))

	ws.Route(ws.GET("/users/{user}/loginrecords").
		To(handler.ListUserLoginRecords).
		Param(ws.PathParameter("user", "username of the user")).
		Doc("List login records of the specified user.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{Items: []runtime.Object{&iamv1beta1.LoginRecord{}}}).
		Metadata(restfulspec.KeyOpenAPITags, []string{constants.UserResourceTag}))

	ws.Route(ws.GET("/workspaces/{workspace}/groups").
		To(handler.ListWorkspaceGroups).
		Param(ws.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}).
		Doc("List groups of the specified workspace."))

	ws.Route(ws.GET("/workspaces/{workspace}/groups/{group}").
		To(handler.DescribeGroup).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Retrieve group details.").
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.Group{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/groups/{group}").
		To(handler.DeleteGroup).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Delete group.").
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.POST("/workspaces/{workspace}/groups").
		To(handler.CreateGroup).
		Param(ws.PathParameter("workspace", "workspace name")).
		Doc("Create Group").
		Reads(iamv1beta1.Group{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.Group{}))

	ws.Route(ws.PUT("/workspaces/{workspace}/groups/{group}/").
		To(handler.UpdateGroup).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Update Group").
		Reads(iamv1beta1.Group{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.Group{}))

	ws.Route(ws.PATCH("/workspaces/{workspace}/groups/{group}/").
		To(handler.PatchGroup).
		Param(ws.PathParameter("workspace", "workspace name")).
		Doc("Patch Group").
		Reads(iamv1beta1.Group{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.Group{}))

	ws.Route(ws.GET("/workspaces/{workspace}/groupbindings").
		To(handler.ListGroupBindings).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Retrieve group's members in the workspace.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.GET("/workspaces/{workspace}/rolebindings").
		To(handler.ListGroupRoleBindings).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Retrieve group's rolebindings of all projects in the workspace.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.GET("/workspaces/{workspace}/workspacerolebindings").
		To(handler.ListGroupWorkspaceRoleBindings).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("group", "group name")).
		Doc("Retrieve group's workspacerolebindings of the workspace.").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	ws.Route(ws.DELETE("/workspaces/{workspace}/groupbindings/{groupbinding}").
		To(handler.DeleteGroupBinding).
		Param(ws.PathParameter("workspace", "workspace name")).
		Param(ws.PathParameter("groupbinding", "groupbinding name")).
		Doc("Delete GroupBinding to remove user from the group.").
		Returns(http.StatusOK, api.StatusOK, errors.None))

	ws.Route(ws.POST("/workspaces/{workspace}/groupbindings").
		To(handler.CreateGroupBinding).
		Param(ws.PathParameter("workspace", "workspace name")).
		Doc("Create GroupBinding to add a user to the group").
		Reads([]GroupMember{}).
		Returns(http.StatusOK, api.StatusOK, iamv1beta1.GroupBinding{}))

	container.Add(ws)
	return nil
}
