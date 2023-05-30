package v1beta1

import (
	"fmt"

	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"

	"k8s.io/apiserver/pkg/endpoints/request"

	"github.com/emicklei/go-restful/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/auth"
	servererr "kubesphere.io/kubesphere/pkg/server/errors"

	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/rbac"
	apirequest "kubesphere.io/kubesphere/pkg/apiserver/request"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
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
	im         im.IdentityManagementInterface
	am         am.AccessManagementInterface
	authorizer authorizer.Authorizer
}

func newIAMHandler(im im.IdentityManagementInterface, am am.AccessManagementInterface) *iamHandler {
	return &iamHandler{
		im:         im,
		am:         am,
		authorizer: rbac.NewRBACAuthorizer(am),
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

	response.WriteEntity(user)
}

func (h *iamHandler) ListUsers(request *restful.Request, response *restful.Response) {
	globalroleSpecific := request.QueryParameter("globalrole")
	if globalroleSpecific != "" {
		result, err := h.listUserByGlobalRole(globalroleSpecific)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		response.WriteEntity(result)
		return
	}

	result, err := h.im.ListUsers(query.New())
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	for i, item := range result.Items {
		user := item.(*iamv1beta1.User)
		user = user.DeepCopy()
		globalRole, err := h.am.GetGlobalRoleOfUser(user.Name)

		// ignore not found error
		if err != nil && !errors.IsNotFound(err) {
			api.HandleInternalError(response, request, err)
			return
		}
		if globalRole != nil {
			user = appendGlobalRoleAnnotation(user, globalRole.Name)
		}
		result.Items[i] = user
	}
	response.WriteEntity(result)
}

func (h *iamHandler) listUserByGlobalRole(roleName string) (*api.ListResult, error) {
	result := &api.ListResult{}
	bindings, err := h.am.ListGlobalRoleBindings("", roleName)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					return nil, err
				}
				user.Annotations[iamv1beta1.GlobalRoleAnnotation] = binding.RoleRef.Name
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}
	return result, nil
}

func (h *iamHandler) CreateUser(req *restful.Request, resp *restful.Response) {
	var user iamv1beta1.User
	err := req.ReadEntity(&user)
	if err != nil {
		api.HandleBadRequest(resp, req, err)
		return
	}
	operator, ok := request.UserFrom(req.Request.Context())
	if ok && operator.GetName() == iamv1beta1.PreRegistrationUser {
		extra := operator.GetExtra()
		// The token used for registration must contain additional information
		if len(extra[iamv1beta1.ExtraIdentityProvider]) != 1 || len(extra[iamv1beta1.ExtraUID]) != 1 {
			err = errors.NewBadRequest("invalid registration token")
			api.HandleBadRequest(resp, req, err)
			return
		}
		if user.Labels == nil {
			user.Labels = make(map[string]string)
		}
		user.Labels[iamv1beta1.IdentifyProviderLabel] = extra[iamv1beta1.ExtraIdentityProvider][0]
		user.Labels[iamv1beta1.OriginUIDLabel] = extra[iamv1beta1.ExtraUID][0]
		// default role
		delete(user.Annotations, iamv1beta1.GlobalRoleAnnotation)
	}

	globalRole := user.Annotations[iamv1beta1.GlobalRoleAnnotation]
	delete(user.Annotations, iamv1beta1.GlobalRoleAnnotation)
	if globalRole != "" {
		if _, err = h.am.GetGlobalRole(globalRole); err != nil {
			api.HandleError(resp, req, err)
			return
		}
	}

	created, err := h.im.CreateUser(&user)
	if err != nil {
		api.HandleError(resp, req, err)
		return
	}

	if globalRole != "" {
		if err := h.am.CreateGlobalRoleBinding(user.Name, globalRole); err != nil {
			api.HandleError(resp, req, err)
			return
		}
	}

	// ensure encrypted password will not be output
	created.Spec.EncryptedPassword = ""

	resp.WriteEntity(created)
}

func (h *iamHandler) UpdateUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	var user iamv1beta1.User

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

	globalRole := user.Annotations[iamv1beta1.GlobalRoleAnnotation]
	delete(user.Annotations, iamv1beta1.GlobalRoleAnnotation)

	updated, err := h.im.UpdateUser(&user)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	operator, ok := apirequest.UserFrom(request.Request.Context())
	if globalRole != "" && ok {
		err = h.updateGlobalRoleBinding(operator, updated, globalRole)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
		updated = appendGlobalRoleAnnotation(updated, globalRole)
	}

	response.WriteEntity(updated)
}

func appendGlobalRoleAnnotation(user *iamv1beta1.User, globalRole string) *iamv1beta1.User {
	if user.Annotations == nil {
		user.Annotations = make(map[string]string, 0)
	}
	user.Annotations[iamv1beta1.GlobalRoleAnnotation] = globalRole
	return user
}

func (h *iamHandler) updateGlobalRoleBinding(operator authuser.Info, user *iamv1beta1.User, globalRole string) error {

	oldGlobalRole, err := h.am.GetGlobalRoleOfUser(user.Name)
	if err != nil && !errors.IsNotFound(err) {
		klog.Error(err)
		return err
	}

	if oldGlobalRole != nil && oldGlobalRole.Name == globalRole {
		return nil
	}

	userManagement := authorizer.AttributesRecord{
		Resource:        iamv1beta1.ResourcesPluralUser,
		Verb:            "update",
		ResourceScope:   apirequest.GlobalScope,
		ResourceRequest: true,
		User:            operator,
	}
	decision, _, err := h.authorizer.Authorize(userManagement)
	if err != nil {
		klog.Error(err)
		return err
	}
	if decision != authorizer.DecisionAllow {
		err = errors.NewForbidden(iamv1beta1.Resource(iamv1beta1.ResourcesSingularUser),
			user.Name, fmt.Errorf("update global role binding is not allowed"))
		klog.Warning(err)
		return err
	}
	if err := h.am.CreateGlobalRoleBinding(user.Name, globalRole); err != nil {
		klog.Error(err)
		return err
	}
	return nil
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

	response.WriteEntity(servererr.None)
}

func (h *iamHandler) DeleteUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	err := h.im.DeleteUser(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *iamHandler) ListUserLoginRecords(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")
	queryParam := query.ParseQueryParameter(request)
	result, err := h.im.ListLoginRecords(username, queryParam)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	response.WriteEntity(result)
}

func (h *iamHandler) ListClusterMembers(request *restful.Request, response *restful.Response) {
	roleName := request.QueryParameter("clusterrole")
	bindings, err := h.am.ListClusterRoleBindings("", roleName)
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.ClusterRoleAnnotation] = binding.RoleRef.Name
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) ListWorkspaceMembers(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	roleName := request.QueryParameter("workspacerole")
	bindings, err := h.am.ListWorkspaceRoleBindings("", roleName, nil, workspace)
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.WorkspaceRoleAnnotation] = binding.RoleRef.Name
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) ListNamespaceMembers(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	roleName := request.QueryParameter("role")
	bindings, err := h.am.ListRoleBindings("", roleName, nil, namespace)
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.RoleAnnotation] = binding.RoleRef.Name
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) CreateClusterMembers(request *restful.Request, response *restful.Response) {
	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateClusterRoleBinding(member.Username, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *iamHandler) RemoveClusterMember(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("clustermember")

	err := h.am.RemoveUserFromCluster(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *iamHandler) CreateNamespaceMembers(request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")

	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateNamespaceRoleBinding(member.Username, namespace, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *iamHandler) RemoveNamespaceMember(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("member")
	namespace := request.PathParameter("namespace")

	err := h.am.RemoveUserFromNamespace(username, namespace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *iamHandler) GetGlobalRoleOfUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("username")
	globalRole, err := h.am.GetGlobalRoleOfUser(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(globalRole)
}

func (h *iamHandler) GetWorkspaceRoleOfUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("username")
	workspace := request.PathParameter("workspace")
	result := &api.ListResult{Items: make([]runtime.Object, 0)}

	workspaceRoles, err := h.am.GetWorkspaceRoleOfUser(username, nil, workspace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	for _, role := range workspaceRoles {
		result.Items = append(result.Items, role)
		result.TotalItems += 1
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) GetClusterRoleOfUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("username")

	clusterRole, err := h.am.GetClusterRoleOfUser(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(clusterRole)
}

func (h *iamHandler) GetRoleOfUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("username")
	namespace := request.PathParameter("namespace")
	result := &api.ListResult{Items: make([]runtime.Object, 0)}

	roles, err := h.am.GetNamespaceRoleOfUser(username, nil, namespace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	for _, role := range roles {
		result.Items = append(result.Items, role)
		result.TotalItems += 1
	}

	_ = response.WriteEntity(result)

}

func (h *iamHandler) ListRoleTemplateOfUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("username")
	scope := request.QueryParameter("scope")
	namespace := request.QueryParameter("namespace")
	workspace := request.QueryParameter("workspace")

	userInfo, exist := apirequest.UserFrom(request.Request.Context())
	if !exist {
		err := errors.NewInternalError(fmt.Errorf("cannot obtain user info"))
		api.HandleInternalError(response, request, err)
		return
	}

	if userInfo.GetName() != username {
		userManagement := authorizer.AttributesRecord{
			Resource:        "users",
			Verb:            "get",
			ResourceScope:   apirequest.GlobalScope,
			ResourceRequest: true,
			User:            userInfo,
		}

		decision, _, err := h.authorizer.Authorize(userManagement)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}

		if decision != authorizer.DecisionAllow {
			err := errors.NewForbidden(iamv1beta1.Resource("users"), username, fmt.Errorf("not allow to get users"))
			api.HandleError(response, request, err)
			return
		}

	}

	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	var roleTemplateNames []string

	switch scope {
	case iamv1beta1.ScopeGlobal:
		globalRole, err := h.am.GetGlobalRoleOfUser(username)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
		if globalRole.AggregationRoleTemplates != nil {
			roleTemplateNames = globalRole.AggregationRoleTemplates.TemplateNames
		}
	case iamv1beta1.ScopeWorkspace:
		workspaceRoles, err := h.am.GetWorkspaceRoleOfUser(username, nil, workspace)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		for _, workspaceRole := range workspaceRoles {
			if workspaceRole.AggregationRoleTemplates != nil {
				roleTemplateNames = append(roleTemplateNames, workspaceRole.AggregationRoleTemplates.TemplateNames...)
			}
		}

	case iamv1beta1.ScopeCluster:
		clusterRole, err := h.am.GetClusterRoleOfUser(username)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		if clusterRole.AggregationRoleTemplates != nil {
			roleTemplateNames = clusterRole.AggregationRoleTemplates.TemplateNames
		}

	case iamv1beta1.ScopeNamespace:
		roles, err := h.am.GetNamespaceRoleOfUser(username, nil, namespace)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		for _, role := range roles {
			if role.AggregationRoleTemplates != nil {
				roleTemplateNames = append(roleTemplateNames, role.AggregationRoleTemplates.TemplateNames...)
			}
		}
	}

	for _, name := range roleTemplateNames {
		template, err := h.am.GetRoleTemplate(name)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		result.Items = append(result.Items, template)
		result.TotalItems += 1
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) CreateSubjectAccessReview(request *restful.Request, response *restful.Response) {
	subjectAccessReview := iamv1beta1.SubjectAccessReview{}
	if err := request.ReadEntity(&subjectAccessReview); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	attr := authorizer.AttributesRecord{
		User: &iamv1beta1.DefaultInfo{
			Name:   subjectAccessReview.Spec.User,
			UID:    subjectAccessReview.Spec.UID,
			Groups: subjectAccessReview.Spec.Groups,
		},
	}

	if subjectAccessReview.Spec.ResourceAttributes != nil {
		attr.ResourceRequest = true
		attr.Verb = subjectAccessReview.Spec.ResourceAttributes.Verb
		attr.APIGroup = subjectAccessReview.Spec.ResourceAttributes.Group
		attr.APIVersion = subjectAccessReview.Spec.ResourceAttributes.Version
		attr.Workspace = subjectAccessReview.Spec.ResourceAttributes.Workspace
		attr.Namespace = subjectAccessReview.Spec.ResourceAttributes.Namespace
		attr.Resource = subjectAccessReview.Spec.ResourceAttributes.Resource
		attr.Name = subjectAccessReview.Spec.ResourceAttributes.Name
		attr.Subresource = subjectAccessReview.Spec.ResourceAttributes.Subresource
		if attr.Namespace != "" {
			attr.ResourceScope = apirequest.NamespaceScope
		} else if attr.Workspace != "" {
			attr.ResourceScope = apirequest.WorkspaceScope
		} else {
			attr.ResourceScope = subjectAccessReview.Spec.ResourceAttributes.ResourceScope
		}
	} else {
		attr.ResourceRequest = false
		attr.Verb = subjectAccessReview.Spec.NonResourceAttributes.Verb
		attr.Path = subjectAccessReview.Spec.NonResourceAttributes.Path
	}

	decision, reason, err := h.authorizer.Authorize(attr)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	subjectAccessReview.Status = iamv1beta1.SubjectAccessReviewStatus{}

	switch decision {
	case authorizer.DecisionAllow:
		subjectAccessReview.Status.Allowed = true
	case authorizer.DecisionDeny:
		subjectAccessReview.Status.Denied = true
		subjectAccessReview.Status.Allowed = false
		subjectAccessReview.Status.Reason = reason
	case authorizer.DecisionNoOpinion:
		subjectAccessReview.Status.Allowed = false
		subjectAccessReview.Status.Denied = false
		subjectAccessReview.Status.Reason = reason
	}

	_ = response.WriteEntity(subjectAccessReview)
}

func (h *iamHandler) CreateWorkspaceMembers(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")

	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateUserWorkspaceRoleBinding(member.Username, workspace, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *iamHandler) RemoveWorkspaceMember(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	username := request.PathParameter("workspacemember")

	err := h.am.RemoveUserFromWorkspace(username, workspace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}
