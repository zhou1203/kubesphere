package v1beta1

import (
	"github.com/emicklei/go-restful/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/rbac"
	apiserverrequest "kubesphere.io/kubesphere/pkg/apiserver/request"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
)

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

func (h *iamHandler) ListClusterMembers(request *restful.Request, response *restful.Response) {
	bindings, err := h.am.ListClusterRoleBindings("")
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
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) ListWorkspaceMembers(request *restful.Request, response *restful.Response) {
	workspace := request.PathParameter("workspace")
	bindings, err := h.am.ListWorkspaceRoleBindings("", nil, workspace)
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
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
}

func (h *iamHandler) ListNamespaceMember(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	bindings, err := h.am.ListRoleBindings("", nil, namespace)
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
				result.Items = append(result.Items, user)
				result.TotalItems += 1
			}
		}
	}

	_ = response.WriteEntity(result)
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
	result := &api.ListResult{Items: make([]runtime.Object, 0)}
	var roleTemplateNames []string

	switch scope {
	case iamv1beta1.ScopeGlobal:
		globalRole, err := h.am.GetGlobalRoleOfUser(username)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		roleTemplateNames = globalRole.AggregationRoleTemplates.TemplateNames
	case iamv1beta1.ScopeWorkspace:
		workspaceRoles, err := h.am.GetWorkspaceRoleOfUser(username, nil, workspace)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		for _, workspaceRole := range workspaceRoles {
			roleTemplateNames = append(roleTemplateNames, workspaceRole.AggregationRoleTemplates.TemplateNames...)
		}

	case iamv1beta1.ScopeCluster:
		clusterRole, err := h.am.GetClusterRoleOfUser(username)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		roleTemplateNames = clusterRole.AggregationRoleTemplates.TemplateNames

	case iamv1beta1.ScopeNamespace:
		roles, err := h.am.GetNamespaceRoleOfUser(username, nil, namespace)
		if err != nil {
			api.HandleError(response, request, err)
			return
		}

		for _, role := range roles {
			roleTemplateNames = append(roleTemplateNames, role.AggregationRoleTemplates.TemplateNames...)
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
			attr.ResourceScope = apiserverrequest.NamespaceScope
		} else if attr.Workspace != "" {
			attr.ResourceScope = apiserverrequest.WorkspaceScope
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
