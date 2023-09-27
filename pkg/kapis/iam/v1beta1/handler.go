package v1beta1

import (
	"fmt"

	"github.com/emicklei/go-restful/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	apirequest "kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/models/auth"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	resv1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
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

type handler struct {
	im         im.IdentityManagementInterface
	am         am.AccessManagementInterface
	authorizer authorizer.Authorizer
}

func (h *handler) DescribeUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")
	user, err := h.im.DescribeUser(username)
	if err != nil {
		if errors.IsNotFound(err) {
			api.HandleNotFound(response, request, err)
			return
		}
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(user)
}

func (h *handler) ListUsers(request *restful.Request, response *restful.Response) {
	globalRole := request.QueryParameter("globalrole")
	if globalRole != "" {
		result, err := h.listUserByGlobalRole(globalRole)
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		response.WriteEntity(result)
		return
	}
	queryParam := query.ParseQueryParameter(request)
	result, err := h.im.ListUsers(queryParam)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	response.WriteEntity(result)
}

func (h *handler) listUserByGlobalRole(roleName string) (*api.ListResult, error) {
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
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
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}
	return result, nil
}

func (h *handler) CreateUser(req *restful.Request, resp *restful.Response) {
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
	}

	globalRole := user.Annotations[iamv1beta1.GlobalRoleAnnotation]
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
		if err := h.am.CreateOrUpdateGlobalRoleBinding(user.Name, globalRole); err != nil {
			api.HandleError(resp, req, err)
			return
		}
	}

	// ensure encrypted password will not be output
	created.Spec.EncryptedPassword = ""

	resp.WriteEntity(created)
}

func (h *handler) UpdateUser(request *restful.Request, response *restful.Response) {
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

	updated, err := h.im.UpdateUser(&user)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	operator, ok := apirequest.UserFrom(request.Request.Context())
	if globalRole != "" && ok {
		if err = h.updateGlobalRoleBinding(operator, updated, globalRole); err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(updated)
}

func (h *handler) updateGlobalRoleBinding(operator authuser.Info, user *iamv1beta1.User, globalRole string) error {
	oldGlobalRole, err := h.am.GetGlobalRoleOfUser(user.Name)
	if err != nil && !errors.IsNotFound(err) {
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
		return err
	}
	if decision != authorizer.DecisionAllow {
		return errors.NewForbidden(iamv1beta1.Resource(iamv1beta1.ResourcesSingularUser),
			user.Name, fmt.Errorf("update global role binding is not allowed"))
	}
	if err := h.am.CreateOrUpdateGlobalRoleBinding(user.Name, globalRole); err != nil {
		return err
	}
	return nil
}

func (h *handler) ModifyPassword(request *restful.Request, response *restful.Response) {
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

func (h *handler) DeleteUser(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")

	err := h.im.DeleteUser(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) ListUserLoginRecords(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("user")
	queryParam := query.ParseQueryParameter(request)
	result, err := h.im.ListLoginRecords(username, queryParam)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	response.WriteEntity(result)
}

func (h *handler) ListClusterMembers(request *restful.Request, response *restful.Response) {
	roleName := request.QueryParameter("clusterrole")
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	bindings, err := h.am.ListClusterRoleBindings("", roleName)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	users := make([]runtime.Object, 0)
	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					if errors.IsNotFound(err) {
						continue
					}
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.ClusterRoleAnnotation] = binding.RoleRef.Name
				users = append(users, user)
			}
		}
	}

	list, _ := resv1beta1.DefaultList(users, query.ParseQueryParameter(request), resv1beta1.DefaultCompare, resv1beta1.DefaultFilter)
	result.Items = list
	result.TotalItems = len(list)

	_ = response.WriteEntity(result)
}

func (h *handler) ListWorkspaceMembers(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	roleName := request.QueryParameter("workspacerole")
	bindings, err := h.am.ListWorkspaceRoleBindings("", roleName, nil, workspace)
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	users := make([]runtime.Object, 0)
	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					if errors.IsNotFound(err) {
						continue
					}
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.WorkspaceRoleAnnotation] = binding.RoleRef.Name
				users = append(users, user)
			}
		}
	}
	list, _ := resv1beta1.DefaultList(users, query.ParseQueryParameter(request), resv1beta1.DefaultCompare, resv1beta1.DefaultFilter)
	result.Items = list
	result.TotalItems = len(list)

	_ = response.WriteEntity(result)
}

func (h *handler) ListNamespaceMembers(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	roleName := request.QueryParameter("role")
	bindings, err := h.am.ListRoleBindings("", roleName, nil, namespace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	users := make([]runtime.Object, 0)

	for _, binding := range bindings {
		for _, subject := range binding.Subjects {
			if subject.Kind == rbacv1.UserKind {
				user, err := h.im.DescribeUser(subject.Name)
				if err != nil {
					if errors.IsNotFound(err) {
						continue
					}
					api.HandleError(response, request, err)
					return
				}
				user.Annotations[iamv1beta1.RoleAnnotation] = binding.RoleRef.Name
				users = append(users, user)
			}
		}
	}

	list, _ := resv1beta1.DefaultList(users, query.ParseQueryParameter(request), resv1beta1.DefaultCompare, resv1beta1.DefaultFilter)
	result.Items = list
	result.TotalItems = len(list)

	_ = response.WriteEntity(result)
}

func (h *handler) CreateClusterMembers(request *restful.Request, response *restful.Response) {
	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateOrUpdateClusterRoleBinding(member.Username, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *handler) RemoveClusterMember(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("clustermember")

	err := h.am.RemoveUserFromCluster(username)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) CreateNamespaceMembers(request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")

	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateOrUpdateNamespaceRoleBinding(member.Username, namespace, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *handler) RemoveNamespaceMember(request *restful.Request, response *restful.Response) {
	username := request.PathParameter("member")
	namespace := request.PathParameter("namespace")

	err := h.am.RemoveUserFromNamespace(username, namespace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) ListRoleTemplateOfUser(request *restful.Request, response *restful.Response) {
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
		if err != nil && !errors.IsNotFound(err) {
			api.HandleError(response, request, err)
			return
		}
		if globalRole != nil && globalRole.AggregationRoleTemplates != nil {
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
		if err != nil && !errors.IsNotFound(err) {
			api.HandleError(response, request, err)
			return
		}
		if clusterRole != nil && clusterRole.AggregationRoleTemplates != nil {
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
			if errors.IsNotFound(err) {
				continue
			}
			api.HandleError(response, request, err)
			return
		}
		result.Items = append(result.Items, template)
		result.TotalItems += 1
	}

	_ = response.WriteEntity(result)
}

func (h *handler) CreateSubjectAccessReview(request *restful.Request, response *restful.Response) {
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

func (h *handler) CreateWorkspaceMembers(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")

	var members []Member
	err := request.ReadEntity(&members)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	for _, member := range members {
		err := h.am.CreateOrUpdateUserWorkspaceRoleBinding(member.Username, workspace, member.RoleRef)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}
	}

	response.WriteEntity(members)
}

func (h *handler) RemoveWorkspaceMember(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	username := request.PathParameter("workspacemember")

	err := h.am.RemoveUserFromWorkspace(username, workspace)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) DescribeWorkspaceMember(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	memberName := request.PathParameter("workspacemember")
	bindings, err := h.am.ListWorkspaceRoleBindings(memberName, "", nil, workspace)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	if len(bindings) == 0 {
		api.HandleBadRequest(response, request, NewErrMemberNotExist(memberName))
		return
	}

	user, err := h.im.DescribeUser(memberName)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	user.Annotations[iamv1beta1.WorkspaceRoleAnnotation] = bindings[0].RoleRef.Name
	_ = response.WriteEntity(user)
}

func (h *handler) UpdateWorkspaceMember(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	memberName := request.PathParameter("workspacemember")

	var member Member
	err := request.ReadEntity(&member)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if memberName != member.Username {
		api.HandleBadRequest(response, request, NewErrIncorrectUsername(memberName))
		return
	}

	bindings, err := h.am.ListWorkspaceRoleBindings(memberName, "", nil, workspace)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	if len(bindings) == 0 {
		api.HandleBadRequest(response, request, NewErrMemberNotExist(member.Username))
		return
	}

	err = h.am.CreateOrUpdateUserWorkspaceRoleBinding(member.Username, workspace, member.RoleRef)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	response.WriteEntity(servererr.None)
}

func (h *handler) UpdateClusterMember(request *restful.Request, response *restful.Response) {
	memberName := request.PathParameter("clustermember")

	var member Member
	err := request.ReadEntity(&member)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if memberName != member.Username {
		api.HandleBadRequest(response, request, NewErrIncorrectUsername(memberName))
		return
	}

	bindings, err := h.am.ListClusterRoleBindings(memberName, "")
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	if len(bindings) == 0 {
		api.HandleBadRequest(response, request, NewErrMemberNotExist(member.Username))
		return
	}

	err = h.am.CreateOrUpdateClusterRoleBinding(member.Username, member.RoleRef)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func (h *handler) UpdateNamespaceMember(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	memberName := request.PathParameter("namespacemember")

	var member Member
	err := request.ReadEntity(&member)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if memberName != member.Username {
		api.HandleBadRequest(response, request, NewErrIncorrectUsername(memberName))
		return
	}

	bindings, err := h.am.ListRoleBindings(member.Username, "", nil, namespace)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	if len(bindings) == 0 {
		api.HandleBadRequest(response, request, NewErrMemberNotExist(member.Username))
		return
	}

	err = h.am.CreateOrUpdateNamespaceRoleBinding(member.Username, namespace, member.RoleRef)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	response.WriteEntity(servererr.None)
}

func NewErrMemberNotExist(username string) error {
	return fmt.Errorf("member %s not exist", username)
}

func NewErrIncorrectUsername(username string) error {
	return fmt.Errorf("incorrect username %s, the username must equal to the member", username)
}
