/*
Copyright 2020 The KubeSphere Authors.

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

package am

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

type AccessManagementInterface interface {
	GetGlobalRoleOfUser(username string) (*iamv1beta1.GlobalRole, error)
	GetWorkspaceRoleOfUser(username string, groups []string, workspace string) ([]iamv1beta1.WorkspaceRole, error)
	GetNamespaceRoleOfUser(username string, groups []string, namespace string) ([]iamv1beta1.Role, error)
	GetClusterRoleOfUser(username string) (*iamv1beta1.ClusterRole, error)

	ListWorkspaceRoleBindings(username, roleName string, groups []string, workspace string) ([]iamv1beta1.WorkspaceRoleBinding, error)
	ListClusterRoleBindings(username, roleName string) ([]iamv1beta1.ClusterRoleBinding, error)
	ListGlobalRoleBindings(username, roleName string) ([]iamv1beta1.GlobalRoleBinding, error)
	ListRoleBindings(username, roleName string, groups []string, namespace string) ([]iamv1beta1.RoleBinding, error)

	ListRoles(namespace string, query *query.Query) (*api.ListResult, error)
	ListClusterRoles(query *query.Query) (*api.ListResult, error)
	ListWorkspaceRoles(query *query.Query) (*api.ListResult, error)
	ListGlobalRoles(query *query.Query) (*api.ListResult, error)

	GetGlobalRole(globalRole string) (*iamv1beta1.GlobalRole, error)
	GetWorkspaceRole(workspace string, name string) (*iamv1beta1.WorkspaceRole, error)
	GetNamespaceRole(namespace string, name string) (*iamv1beta1.Role, error)
	GetClusterRole(name string) (*iamv1beta1.ClusterRole, error)

	GetRoleReferenceRules(roleRef rbacv1.RoleRef, namespace string) (regoPolicy string, rules []rbacv1.PolicyRule, err error)
	GetNamespaceControlledWorkspace(namespace string) (string, error)

	ListGroupWorkspaceRoleBindings(workspace string, query *query.Query) (*api.ListResult, error)

	ListGroupRoleBindings(workspace string, query *query.Query) ([]iamv1beta1.RoleBinding, error)

	GetRoleTemplate(name string) (*iamv1beta1.RoleTemplate, error)

	CreateGlobalRoleBinding(username string, globalRole string) error
	CreateUserWorkspaceRoleBinding(username string, workspace string, role string) error
	CreateNamespaceRoleBinding(username string, namespace string, role string) error
	CreateClusterRoleBinding(username string, role string) error

	RemoveGlobalRoleBinding(username string) error
	RemoveUserFromWorkspace(username string, workspace string) error
	RemoveUserFromNamespace(username string, namespace string) error
	RemoveUserFromCluster(username string) error
}

type amOperator struct {
	resourceManager resourcev1beta1.ResourceManager
}

func NewReadOnlyOperator(manager resourcev1beta1.ResourceManager) AccessManagementInterface {
	operator := &amOperator{
		resourceManager: manager,
	}

	return operator
}

func NewOperator(manager resourcev1beta1.ResourceManager) AccessManagementInterface {
	amOperator := NewReadOnlyOperator(manager).(*amOperator)
	return amOperator
}

func (am *amOperator) GetGlobalRoleOfUser(username string) (*iamv1beta1.GlobalRole, error) {
	globalRoleBindings, err := am.ListGlobalRoleBindings(username, "")
	if err != nil {
		return nil, err
	}
	if len(globalRoleBindings) > 1 {
		klog.Warningf("conflict global role binding, username: %s", username)
	}
	if len(globalRoleBindings) > 0 {
		return am.GetGlobalRole(globalRoleBindings[0].RoleRef.Name)
	}
	return nil, errors.NewNotFound(iamv1beta1.Resource(iamv1beta1.ResourcesSingularGlobalRoleBinding), username)
}

func (am *amOperator) GetWorkspaceRoleOfUser(username string, groups []string, workspace string) ([]iamv1beta1.WorkspaceRole, error) {
	userRoleBindings, err := am.ListWorkspaceRoleBindings(username, "", groups, workspace)
	if err != nil {
		return nil, err
	}
	if len(userRoleBindings) > 0 {
		roles := make([]iamv1beta1.WorkspaceRole, 0)
		for _, roleBinding := range userRoleBindings {
			role, err := am.GetWorkspaceRole(workspace, roleBinding.RoleRef.Name)
			if err != nil {
				return nil, err
			}
			roles = append(roles, *role)
		}
		if len(userRoleBindings) > 1 && workspace != "" {
			klog.Infof("conflict workspace role binding, username: %s", username)
		}
		return roles, nil
	}
	return []iamv1beta1.WorkspaceRole{}, nil
}

func (am *amOperator) GetNamespaceRoleOfUser(username string, groups []string, namespace string) ([]iamv1beta1.Role, error) {
	userRoleBindings, err := am.ListRoleBindings(username, "", groups, namespace)
	if err != nil {
		return nil, err
	}
	if len(userRoleBindings) > 1 && namespace != "" {
		klog.Warningf("conflict role binding found: %v", userRoleBindings)
	}
	if len(userRoleBindings) > 0 {
		roles := make([]iamv1beta1.Role, 0)
		for _, roleBinding := range userRoleBindings {
			role, err := am.GetNamespaceRole(roleBinding.Namespace, roleBinding.RoleRef.Name)
			if err != nil {
				return nil, err
			}
			roles = append(roles, *role)
		}
		return roles, nil
	}
	return nil, err
}

func (am *amOperator) GetClusterRoleOfUser(username string) (*iamv1beta1.ClusterRole, error) {
	roleBindings, err := am.ListClusterRoleBindings(username, "")
	if err != nil {
		return nil, err
	}
	if len(roleBindings) > 1 {
		klog.Warningf("conflict cluster role binding found: %v", roleBindings)
	}
	if len(roleBindings) > 0 {
		return am.GetClusterRole(roleBindings[0].RoleRef.Name)
	}
	return nil, errors.NewNotFound(iamv1beta1.Resource(iamv1beta1.ResourcesSingularClusterRoleBinding), username)
}

func (am *amOperator) ListWorkspaceRoleBindings(username, roleName string, groups []string, workspace string) ([]iamv1beta1.WorkspaceRoleBinding, error) {
	roleBindings := &iamv1beta1.WorkspaceRoleBindingList{}
	queryParam := query.New()
	if workspace != "" {
		if err := queryParam.AddLabels(map[string]string{tenantv1alpha1.WorkspaceLabel: workspace}); err != nil {
			return nil, err
		}
	}

	if username != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.UserReferenceLabel: username}); err != nil {
			return nil, err
		}
	}

	if roleName != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.RoleReferenceLabel: roleName}); err != nil {
			return nil, err
		}
	}

	if err := am.resourceManager.List(context.Background(), "", queryParam, roleBindings); err != nil {
		return nil, err
	}

	result := make([]iamv1beta1.WorkspaceRoleBinding, 0)
	for i, roleBinding := range roleBindings.Items {
		inSpecifiedWorkspace := workspace == "" || roleBinding.Labels[tenantv1alpha1.WorkspaceLabel] == workspace
		if contains(roleBinding.Subjects, username, groups) && inSpecifiedWorkspace {
			result = append(result, roleBindings.Items[i])
		}
	}

	return result, nil
}

func (am *amOperator) ListClusterRoleBindings(username, roleName string) ([]iamv1beta1.ClusterRoleBinding, error) {
	roleBindings := &iamv1beta1.ClusterRoleBindingList{}
	queryParam := query.New()
	if username != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.UserReferenceLabel: username}); err != nil {
			return nil, err
		}
	}

	if roleName != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.RoleReferenceLabel: roleName}); err != nil {
			return nil, err
		}
	}

	if err := am.resourceManager.List(context.Background(), "", queryParam, roleBindings); err != nil {
		return nil, err
	}

	result := make([]iamv1beta1.ClusterRoleBinding, 0)
	for i := range roleBindings.Items {
		result = append(result, roleBindings.Items[i])
	}

	return result, nil
}

func (am *amOperator) ListGlobalRoleBindings(username, roleName string) ([]iamv1beta1.GlobalRoleBinding, error) {
	roleBindings := &iamv1beta1.GlobalRoleBindingList{}
	queryParam := query.New()
	if username != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.UserReferenceLabel: username}); err != nil {
			return nil, err
		}
	}

	if roleName != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.RoleReferenceLabel: roleName}); err != nil {
			return nil, err
		}
	}

	if err := am.resourceManager.List(context.Background(), "", queryParam, roleBindings); err != nil {
		return nil, err
	}

	result := make([]iamv1beta1.GlobalRoleBinding, 0)
	for i := range roleBindings.Items {
		result = append(result, roleBindings.Items[i])
	}

	return result, nil
}

func (am *amOperator) ListRoleBindings(username, roleName string, groups []string, namespace string) ([]iamv1beta1.RoleBinding, error) {
	roleBindings := &iamv1beta1.RoleBindingList{}
	queryParam := query.New()
	if username != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.UserReferenceLabel: username}); err != nil {
			return nil, err
		}
	}

	if roleName != "" {
		if err := queryParam.AddLabels(map[string]string{iamv1beta1.RoleReferenceLabel: roleName}); err != nil {
			return nil, err
		}
	}

	if err := am.resourceManager.List(context.Background(), namespace, queryParam, roleBindings); err != nil {
		return nil, err
	}

	result := make([]iamv1beta1.RoleBinding, 0)
	for i, roleBinding := range roleBindings.Items {
		if contains(roleBinding.Subjects, username, groups) {
			result = append(result, roleBindings.Items[i])
		}
	}
	return result, nil
}

func contains(subjects []rbacv1.Subject, username string, groups []string) bool {
	// if username is nil means list all role bindings
	if username == "" {
		return true
	}
	for _, subject := range subjects {
		if subject.Kind == rbacv1.UserKind && subject.Name == username {
			return true
		}
		if subject.Kind == rbacv1.GroupKind && sliceutil.HasString(groups, subject.Name) {
			return true
		}
	}
	return false
}

func (am *amOperator) ListRoles(namespace string, query *query.Query) (*api.ListResult, error) {
	roleList := &iamv1beta1.RoleList{}
	if err := am.resourceManager.List(context.Background(), namespace, query, roleList); err != nil {
		return nil, err
	}
	return convertToListResult(roleList)
}

func (am *amOperator) ListClusterRoles(query *query.Query) (*api.ListResult, error) {
	roleList := &iamv1beta1.ClusterRoleList{}
	if err := am.resourceManager.List(context.Background(), "", query, roleList); err != nil {
		return nil, err
	}
	return convertToListResult(roleList)
}

func convertToListResult(list client.ObjectList) (*api.ListResult, error) {
	listResult := &api.ListResult{}
	extractList, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}

	listResult.Items = extractList
	listResult.TotalItems = len(extractList)
	return listResult, nil

}

func (am *amOperator) ListWorkspaceRoles(query *query.Query) (*api.ListResult, error) {
	roleList := &iamv1beta1.WorkspaceRoleList{}
	if err := am.resourceManager.List(context.Background(), "", query, roleList); err != nil {
		return nil, err
	}
	return convertToListResult(roleList)
}

func (am *amOperator) ListGlobalRoles(query *query.Query) (*api.ListResult, error) {
	roleList := &iamv1beta1.GlobalRoleList{}
	if err := am.resourceManager.List(context.Background(), "", query, roleList); err != nil {
		return nil, err
	}
	return convertToListResult(roleList)
}

func (am *amOperator) GetGlobalRole(globalRole string) (*iamv1beta1.GlobalRole, error) {
	role := &iamv1beta1.GlobalRole{}
	if err := am.resourceManager.Get(context.Background(), "", globalRole, role); err != nil {
		return nil, err
	}
	return role, nil
}

// GetRoleReferenceRules attempts to resolve the RoleBinding or ClusterRoleBinding.
func (am *amOperator) GetRoleReferenceRules(roleRef rbacv1.RoleRef, namespace string) (regoPolicy string, rules []rbacv1.PolicyRule, err error) {
	empty := make([]rbacv1.PolicyRule, 0)
	switch roleRef.Kind {
	case iamv1beta1.ResourceKindRole:
		role, err := am.GetNamespaceRole(namespace, roleRef.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return "", empty, nil
			}
			return "", nil, err
		}
		return role.Annotations[iamv1beta1.RegoOverrideAnnotation], role.Rules, nil
	case iamv1beta1.ResourceKindClusterRole:
		clusterRole, err := am.GetClusterRole(roleRef.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return "", empty, nil
			}
			return "", nil, err
		}
		return clusterRole.Annotations[iamv1beta1.RegoOverrideAnnotation], clusterRole.Rules, nil
	case iamv1beta1.ResourceKindGlobalRole:
		globalRole, err := am.GetGlobalRole(roleRef.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return "", empty, nil
			}
			return "", nil, err
		}
		return globalRole.Annotations[iamv1beta1.RegoOverrideAnnotation], globalRole.Rules, nil
	case iamv1beta1.ResourceKindWorkspaceRole:
		workspaceRole, err := am.GetWorkspaceRole("", roleRef.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return "", empty, nil
			}
			return "", nil, err
		}
		return workspaceRole.Annotations[iamv1beta1.RegoOverrideAnnotation], workspaceRole.Rules, nil
	default:
		return "", nil, fmt.Errorf("unsupported role reference kind: %q", roleRef.Kind)
	}
}

func (am *amOperator) GetWorkspaceRole(workspace string, name string) (*iamv1beta1.WorkspaceRole, error) {
	role := &iamv1beta1.WorkspaceRole{}
	if err := am.resourceManager.Get(context.Background(), metav1.NamespaceAll, name, role); err != nil {
		return nil, err
	}
	if workspace != "" && role.Labels[tenantv1alpha1.WorkspaceLabel] != workspace {
		return nil, errors.NewNotFound(iamv1beta1.Resource(iamv1beta1.ResourcesSingularWorkspaceRole), name)
	}
	return role, nil
}

func (am *amOperator) GetNamespaceRole(namespace string, name string) (*iamv1beta1.Role, error) {
	role := &iamv1beta1.Role{}
	if err := am.resourceManager.Get(context.Background(), namespace, name, role); err != nil {
		return nil, err
	}
	return role, nil
}

func (am *amOperator) GetClusterRole(name string) (*iamv1beta1.ClusterRole, error) {
	role := &iamv1beta1.ClusterRole{}
	if err := am.resourceManager.Get(context.Background(), metav1.NamespaceAll, name, role); err != nil {
		return nil, err
	}
	return role, nil
}

func (am *amOperator) GetNamespaceControlledWorkspace(namespace string) (string, error) {
	ns := &v1.Namespace{}
	if err := am.resourceManager.Get(context.Background(), namespace, metav1.NamespaceAll, ns); err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return ns.Labels[tenantv1alpha1.WorkspaceLabel], nil
}

func (am *amOperator) ListGroupWorkspaceRoleBindings(workspace string, query *query.Query) (*api.ListResult, error) {
	roleList := &iamv1beta1.WorkspaceRoleBindingList{}
	workspaceRequirement, err := labels.NewRequirement(tenantv1alpha1.WorkspaceLabel, selection.Equals, []string{workspace})
	if err != nil {
		return nil, err
	}
	query.Selector().Add(*workspaceRequirement)
	if err = am.resourceManager.List(context.Background(), metav1.NamespaceAll, query, roleList); err != nil {
		return nil, err
	}
	return convertToListResult(roleList)
}

func (am *amOperator) ListGroupRoleBindings(workspace string, query *query.Query) ([]iamv1beta1.RoleBinding, error) {
	if workspace != "" {
		if err := query.AddLabels(map[string]string{tenantv1alpha1.WorkspaceLabel: workspace}); err != nil {
			return nil, err
		}
	}

	namespaces := &v1.NamespaceList{}
	if err := am.resourceManager.List(context.Background(), metav1.NamespaceAll, query, namespaces); err != nil {
		return nil, err
	}

	result := make([]iamv1beta1.RoleBinding, 0)
	for _, namespace := range namespaces.Items {
		roleBindingList := &iamv1beta1.RoleBindingList{}
		if err := am.resourceManager.List(context.Background(), namespace.Name, query, roleBindingList); err != nil {
			return nil, err
		}
		result = append(result, roleBindingList.Items...)
	}

	return result, nil
}

func (am *amOperator) CreateGlobalRoleBinding(username string, role string) error {
	if _, err := am.GetGlobalRole(role); err != nil {
		return err
	}

	roleBindings, err := am.ListGlobalRoleBindings(username, "")
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if role == roleBinding.RoleRef.Name {
			return nil
		}
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}

	globalRoleBinding := iamv1beta1.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", username, role),
			Labels: map[string]string{iamv1beta1.UserReferenceLabel: username,
				iamv1beta1.RoleReferenceLabel: role},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
				Name:     username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindGlobalRole,
			Name:     role,
		},
	}

	return am.resourceManager.Create(context.Background(), &globalRoleBinding)
}

func (am *amOperator) CreateUserWorkspaceRoleBinding(username string, workspace string, role string) error {
	if _, err := am.GetWorkspaceRole(workspace, role); err != nil {
		return err
	}

	roleBindings, err := am.ListWorkspaceRoleBindings(username, "", nil, workspace)
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if role == roleBinding.RoleRef.Name {
			return nil
		}
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}

	roleBinding := iamv1beta1.WorkspaceRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", username, role),
			Labels: map[string]string{iamv1beta1.UserReferenceLabel: username,
				iamv1beta1.RoleReferenceLabel: role,
				tenantv1alpha1.WorkspaceLabel: workspace},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
				Name:     username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindWorkspaceRole,
			Name:     role,
		},
	}

	return am.resourceManager.Create(context.Background(), &roleBinding)
}

func (am *amOperator) CreateNamespaceRoleBinding(username string, namespace string, role string) error {
	if _, err := am.GetNamespaceRole(namespace, role); err != nil {
		return err
	}

	// Don't pass user's groups.
	roleBindings, err := am.ListRoleBindings(username, "", nil, namespace)
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if role == roleBinding.RoleRef.Name {
			return nil
		}
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}

	roleBinding := iamv1beta1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", username, role),
			Namespace: namespace,
			Labels: map[string]string{iamv1beta1.UserReferenceLabel: username,
				iamv1beta1.RoleReferenceLabel: role},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
				Name:     username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindRole,
			Name:     role,
		},
	}

	if err := am.resourceManager.Create(context.Background(), &roleBinding); err != nil {
		return err
	}

	return nil
}

func (am *amOperator) CreateClusterRoleBinding(username string, role string) error {
	if _, err := am.GetClusterRole(role); err != nil {
		return err
	}

	roleBindings, err := am.ListClusterRoleBindings(username, "")
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if role == roleBinding.RoleRef.Name {
			return nil
		}
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}

	roleBinding := iamv1beta1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", username, role),
			Labels: map[string]string{iamv1beta1.UserReferenceLabel: username,
				iamv1beta1.RoleReferenceLabel: role},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
				Name:     username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindClusterRole,
			Name:     role,
		},
	}

	if err := am.resourceManager.Create(context.Background(), &roleBinding); err != nil {
		return err
	}

	return nil
}

func (am *amOperator) RemoveUserFromWorkspace(username string, workspace string) error {
	roleBindings, err := am.ListWorkspaceRoleBindings(username, "", nil, workspace)
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (am *amOperator) RemoveUserFromNamespace(username string, namespace string) error {
	roleBindings, err := am.ListRoleBindings(username, "", nil, namespace)
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindings {
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (am *amOperator) RemoveUserFromCluster(username string) error {
	roleBindings, err := am.ListClusterRoleBindings(username, "")
	if err != nil {
		return err
	}
	for _, roleBinding := range roleBindings {
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (am *amOperator) RemoveGlobalRoleBinding(username string) error {
	roleBindings, err := am.ListGlobalRoleBindings(username, "")
	if err != nil {
		return err
	}
	for _, roleBinding := range roleBindings {
		if err := am.resourceManager.Delete(context.Background(), &roleBinding); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (am *amOperator) GetRoleTemplate(name string) (*iamv1beta1.RoleTemplate, error) {
	roleTemplate := &iamv1beta1.RoleTemplate{}
	if err := am.resourceManager.Get(context.Background(), "", name, roleTemplate); err != nil {
		return nil, err
	}
	return roleTemplate, nil
}
