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
	"fmt"
	"strings"

	"github.com/emicklei/go-restful/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	apirequest "kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/models/auth"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	servererr "kubesphere.io/kubesphere/pkg/server/errors"
)

type Member struct {
	Username string `json:"username"`
	RoleRef  string `json:"roleRef"`
}

type GroupMember struct {
	UserName  string `json:"userName"`
	GroupName string `json:"groupName"`
}

type PasswordReset struct {
	CurrentPassword string `json:"currentPassword"`
	Password        string `json:"password"`
}

type iamHandler struct {
	am         am.LegacyAccessManagementInterface
	im         im.LegacyIdentityManagementInterface
	authorizer authorizer.Authorizer
}

func newIAMHandler(im im.LegacyIdentityManagementInterface, am am.LegacyAccessManagementInterface, authorizer authorizer.Authorizer) *iamHandler {
	return &iamHandler{
		am:         am,
		im:         im,
		authorizer: authorizer,
	}
}

func (h *iamHandler) DescribeUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	user, err := h.im.DescribeUser(username)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	globalRole, err := h.am.GetGlobalRoleOfUser(username)
	// ignore not found error
	if err != nil && !errors.IsNotFound(err) {
		api.HandleInternalError(response, request, err)
		return
	}
	if globalRole != nil {
		user = appendGlobalRoleAnnotation(user, globalRole.Name)
	}

	_ = response.WriteEntity(user)
}

func (h *iamHandler) RetrieveMemberRoleTemplates(request *restful.Request, response *restful.Response) {
	if strings.HasSuffix(request.Request.URL.Path, iamv1alpha2.ResourcesPluralGlobalRole) {
		username := request.PathParameter("user")
		globalRole, err := h.am.GetGlobalRoleOfUser(username)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		if globalRole == nil {
			_ = response.WriteEntity([]iamv1alpha2.GlobalRole{})
			return
		}
		result, err := h.am.ListAggregatedGlobalRoleTemplates(globalRole)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		_ = response.WriteEntity(result)
		return
	}

	if strings.HasSuffix(request.Request.URL.Path, iamv1alpha2.ResourcesPluralClusterRole) {
		username := request.PathParameter("clustermember")
		clusterRole, err := h.am.GetClusterRoleOfUser(username)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		if clusterRole == nil {
			_ = response.WriteEntity([]rbacv1.ClusterRole{})
			return
		}
		result, err := h.am.ListAggregatedClusterRoleTemplates(clusterRole)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		_ = response.WriteEntity(result)
		return
	}

	if strings.HasSuffix(request.Request.URL.Path, iamv1alpha2.ResourcesPluralWorkspaceRole) {
		workspace := request.PathParameter("workspace")
		username := request.PathParameter("workspacemember")
		workspaceRole, err := h.am.GetWorkspaceRoleOfUser(username, workspace)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		if workspaceRole == nil {
			_ = response.WriteEntity([]iamv1alpha2.WorkspaceRole{})
			return
		}
		result, err := h.am.ListAggregatedWorkspaceRoleTemplates(workspaceRole)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		_ = response.WriteEntity(result)
		return
	}

	if strings.HasSuffix(request.Request.URL.Path, iamv1alpha2.ResourcesPluralRole) {
		namespace := request.PathParameter("namespace")
		username := request.PathParameter("member")
		role, err := h.am.GetNamespaceRoleOfUser(username, namespace)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		if role == nil {
			_ = response.WriteEntity([]rbacv1.Role{})
			return
		}
		result, err := h.am.ListAggregatedRoleTemplates(role)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		_ = response.WriteEntity(result)
		return
	}
}

func (h *iamHandler) ListUsers(request *restful.Request, response *restful.Response) {
	queryParam := query.ParseQueryParameter(request)
	result, err := h.im.ListUsers(queryParam)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	for i, item := range result.Items {
		user := item.(*iamv1alpha2.User)
		user = user.DeepCopy()
		globalRole, err := h.am.GetGlobalRoleOfUser(user.Name)
		// ignore not found error
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		if globalRole != nil {
			user = appendGlobalRoleAnnotation(user, globalRole.Name)
		}
		result.Items[i] = user
	}
	_ = response.WriteEntity(result)
}

func appendGlobalRoleAnnotation(user *iamv1alpha2.User, globalRole string) *iamv1alpha2.User {
	if user.Annotations == nil {
		user.Annotations = make(map[string]string, 0)
	}
	user.Annotations[iamv1alpha2.GlobalRoleAnnotation] = globalRole
	return user
}

func (h *iamHandler) CreateUser(req *restful.Request, resp *restful.Response) {
	var user iamv1alpha2.User
	err := req.ReadEntity(&user)
	if err != nil {
		api.HandleBadRequest(resp, req, err)
		return
	}
	operator, ok := request.UserFrom(req.Request.Context())
	if ok && operator.GetName() == iamv1alpha2.PreRegistrationUser {
		extra := operator.GetExtra()
		// The token used for registration must contain additional information
		if len(extra[iamv1alpha2.ExtraIdentityProvider]) != 1 || len(extra[iamv1alpha2.ExtraUID]) != 1 {
			err = errors.NewBadRequest("invalid registration token")
			api.HandleBadRequest(resp, req, err)
			return
		}
		if user.Labels == nil {
			user.Labels = make(map[string]string)
		}
		user.Labels[iamv1alpha2.IdentifyProviderLabel] = extra[iamv1alpha2.ExtraIdentityProvider][0]
		user.Labels[iamv1alpha2.OriginUIDLabel] = extra[iamv1alpha2.ExtraUID][0]
		// default role
		delete(user.Annotations, iamv1alpha2.GlobalRoleAnnotation)
	}

	created, err := h.im.CreateUser(&user)
	if err != nil {
		api.HandleError(resp, req, err)
		return
	}

	// ensure encrypted password will not be output
	created.Spec.EncryptedPassword = ""

	_ = resp.WriteEntity(created)
}

func (h *iamHandler) UpdateUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	var user iamv1alpha2.User

	err := request.ReadEntity(&user)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if username != user.Name {
		err := fmt.Errorf("the name of the object (%s) does not match the name on the URL (%s)", user.Name, username)
		api.HandleBadRequest(response, request, err)
		return
	}

	delete(user.Annotations, iamv1alpha2.GlobalRoleAnnotation)

	updated, err := h.im.UpdateUser(&user)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(updated)
}

func (h *iamHandler) ModifyPassword(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")
	var passwordReset PasswordReset
	err := request.ReadEntity(&passwordReset)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	operator, ok := apirequest.UserFrom(request.Request.Context())

	if !ok {
		err = errors.NewInternalError(fmt.Errorf("cannot obtain user info"))
		api.HandleInternalError(response, request, err)
		return
	}

	userManagement := authorizer.AttributesRecord{
		Resource:        "users/password",
		Verb:            "update",
		ResourceScope:   apirequest.GlobalScope,
		ResourceRequest: true,
		User:            operator,
	}

	decision, _, err := h.authorizer.Authorize(userManagement)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	// only the user manager can modify the password without verifying the old password
	// if old password is defined must be verified
	if decision != authorizer.DecisionAllow || passwordReset.CurrentPassword != "" {
		if err = h.im.PasswordVerify(username, passwordReset.CurrentPassword); err != nil {
			if err == auth.IncorrectPasswordError {
				err = errors.NewBadRequest("incorrect old password")
			}
			api.HandleError(response, request, err)
			return
		}
	}

	err = h.im.ModifyPassword(username, passwordReset.Password)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(servererr.None)
}

func (h *iamHandler) DeleteUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	err := h.im.DeleteUser(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(servererr.None)
}
