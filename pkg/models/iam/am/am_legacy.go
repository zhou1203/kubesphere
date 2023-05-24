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
	"encoding/json"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type LegacyAccessManagementInterface interface {
	GetGlobalRoleOfUser(username string) (*iamv1alpha2.GlobalRole, error)
	GetWorkspaceRoleOfUser(username string, workspace string) (*iamv1alpha2.WorkspaceRole, error)
	GetClusterRoleOfUser(username string) (*rbacv1.ClusterRole, error)
	GetNamespaceRoleOfUser(username string, namespace string) (*rbacv1.Role, error)
	ListAggregatedRoleTemplates(role *rbacv1.Role) ([]*rbacv1.Role, error)
	ListAggregatedClusterRoleTemplates(role *rbacv1.ClusterRole) ([]*rbacv1.ClusterRole, error)
	ListAggregatedGlobalRoleTemplates(role *iamv1alpha2.GlobalRole) ([]*iamv1alpha2.GlobalRole, error)
	ListAggregatedWorkspaceRoleTemplates(role *iamv1alpha2.WorkspaceRole) ([]*iamv1alpha2.WorkspaceRole, error)
}

type legacyAMOperator struct {
	cache runtimeclient.Reader
}

func (am *legacyAMOperator) ListAggregatedRoleTemplates(role *rbacv1.Role) ([]*rbacv1.Role, error) {
	roles := make([]*rbacv1.Role, 0)
	var roleNames []string
	if err := json.Unmarshal([]byte(role.Annotations[iamv1alpha2.AggregationRolesAnnotation]), &roleNames); err == nil {
		for _, roleName := range roleNames {
			relatedRole := &rbacv1.Role{}
			if err := am.cache.Get(context.Background(), types.NamespacedName{Namespace: role.Namespace, Name: roleName}, relatedRole); err != nil {
				if errors.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			roles = append(roles, relatedRole)
		}
	}
	return roles, nil
}

func (am *legacyAMOperator) ListAggregatedClusterRoleTemplates(role *rbacv1.ClusterRole) ([]*rbacv1.ClusterRole, error) {
	roles := make([]*rbacv1.ClusterRole, 0)
	var roleNames []string
	if err := json.Unmarshal([]byte(role.Annotations[iamv1alpha2.AggregationRolesAnnotation]), &roleNames); err == nil {
		for _, roleName := range roleNames {
			relatedRole := &rbacv1.ClusterRole{}
			if err := am.cache.Get(context.Background(), types.NamespacedName{Name: roleName}, relatedRole); err != nil {
				if errors.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			roles = append(roles, relatedRole)
		}
	}
	return roles, nil
}

func (am *legacyAMOperator) ListAggregatedGlobalRoleTemplates(role *iamv1alpha2.GlobalRole) ([]*iamv1alpha2.GlobalRole, error) {
	roles := make([]*iamv1alpha2.GlobalRole, 0)
	var roleNames []string
	if err := json.Unmarshal([]byte(role.Annotations[iamv1alpha2.AggregationRolesAnnotation]), &roleNames); err == nil {
		for _, roleName := range roleNames {
			relatedRole := &iamv1alpha2.GlobalRole{}
			if err := am.cache.Get(context.Background(), types.NamespacedName{Name: roleName}, relatedRole); err != nil {
				if errors.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			roles = append(roles, relatedRole)
		}
	}
	return roles, nil
}

func (am *legacyAMOperator) ListAggregatedWorkspaceRoleTemplates(role *iamv1alpha2.WorkspaceRole) ([]*iamv1alpha2.WorkspaceRole, error) {
	roles := make([]*iamv1alpha2.WorkspaceRole, 0)
	var roleNames []string
	if err := json.Unmarshal([]byte(role.Annotations[iamv1alpha2.AggregationRolesAnnotation]), &roleNames); err == nil {
		for _, roleName := range roleNames {
			relatedRole := &iamv1alpha2.WorkspaceRole{}
			if err := am.cache.Get(context.Background(), types.NamespacedName{Name: roleName}, relatedRole); err != nil {
				if errors.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			roles = append(roles, relatedRole)
		}
	}
	return roles, nil
}

func NewLegacyOperator(cache runtimeclient.Reader) LegacyAccessManagementInterface {
	operator := &legacyAMOperator{
		cache: cache,
	}
	return operator
}

func (am *legacyAMOperator) getRelatedGlobalRole(username string) (string, error) {
	globalRoleBindings := &iamv1alpha2.GlobalRoleBindingList{}
	if err := am.cache.List(context.Background(), globalRoleBindings); err != nil {
		return "", err
	}
	for _, globalRoleBinding := range globalRoleBindings.Items {
		for _, subject := range globalRoleBinding.Subjects {
			if subject.Kind == "User" && subject.Name == username {
				return globalRoleBinding.RoleRef.Name, nil
			}
		}
	}
	return "", nil

}

func (am *legacyAMOperator) getRelatedWorkspaceRole(workspace, username string) (string, error) {
	workspaceRoleBindings := &iamv1alpha2.WorkspaceRoleBindingList{}
	if err := am.cache.List(context.Background(), workspaceRoleBindings,
		runtimeclient.MatchingLabels{tenantv1alpha1.WorkspaceLabel: workspace}); err != nil {
		return "", err
	}
	for _, workspaceRoleBinding := range workspaceRoleBindings.Items {
		for _, subject := range workspaceRoleBinding.Subjects {
			if subject.Kind == "User" && subject.Name == username {
				return workspaceRoleBinding.RoleRef.Name, nil
			}
		}
	}
	return "", nil
}

func (am *legacyAMOperator) getRelatedNamespaceRole(namespace string, username string) (string, error) {
	roleBindings := &rbacv1.RoleBindingList{}
	if err := am.cache.List(context.Background(), roleBindings, runtimeclient.InNamespace(namespace)); err != nil {
		return "", err
	}
	for _, roleBinding := range roleBindings.Items {
		for _, subject := range roleBinding.Subjects {
			if subject.Kind == "User" && subject.Name == username {
				return roleBinding.RoleRef.Name, nil
			}
		}
	}
	return "", nil
}

func (am *legacyAMOperator) getRelatedClusterRole(username string) (string, error) {
	roleBindings := &rbacv1.ClusterRoleBindingList{}
	if err := am.cache.List(context.Background(), roleBindings); err != nil {
		return "", err
	}
	for _, roleBinding := range roleBindings.Items {
		for _, subject := range roleBinding.Subjects {
			if subject.Kind == "User" && subject.Name == username {
				return roleBinding.RoleRef.Name, nil
			}
		}
	}
	return "", nil
}

func (am *legacyAMOperator) GetGlobalRoleOfUser(username string) (*iamv1alpha2.GlobalRole, error) {
	relatedGlobalRoleName, err := am.getRelatedGlobalRole(username)
	if err != nil {
		return nil, err
	}
	if relatedGlobalRoleName == "" {
		return nil, nil
	}
	globalRole := &iamv1alpha2.GlobalRole{}
	err = am.cache.Get(context.Background(), types.NamespacedName{Name: relatedGlobalRoleName}, globalRole)
	return globalRole, runtimeclient.IgnoreNotFound(err)
}

func (am *legacyAMOperator) GetWorkspaceRoleOfUser(username string, workspace string) (*iamv1alpha2.WorkspaceRole, error) {
	relatedWorkspaceRoleName, err := am.getRelatedWorkspaceRole(workspace, username)
	if err != nil {
		return nil, err
	}
	if relatedWorkspaceRoleName == "" {
		return nil, nil
	}
	workspaceRole := &iamv1alpha2.WorkspaceRole{}
	err = am.cache.Get(context.Background(), types.NamespacedName{Name: relatedWorkspaceRoleName}, workspaceRole)
	return workspaceRole, runtimeclient.IgnoreNotFound(err)
}

func (am *legacyAMOperator) GetNamespaceRoleOfUser(username string, namespace string) (*rbacv1.Role, error) {
	relatedNamespaceRoleName, err := am.getRelatedNamespaceRole(namespace, username)
	if err != nil {
		return nil, err
	}
	if relatedNamespaceRoleName == "" {
		return nil, nil
	}
	role := &rbacv1.Role{}
	err = am.cache.Get(context.Background(), types.NamespacedName{Name: relatedNamespaceRoleName, Namespace: namespace}, role)
	return role, runtimeclient.IgnoreNotFound(err)
}

func (am *legacyAMOperator) GetClusterRoleOfUser(username string) (*rbacv1.ClusterRole, error) {
	relatedClusterRoleName, err := am.getRelatedClusterRole(username)
	if err != nil {
		return nil, err
	}
	if relatedClusterRoleName == "" {
		return nil, nil
	}
	role := &rbacv1.ClusterRole{}
	err = am.cache.Get(context.Background(), types.NamespacedName{Name: relatedClusterRoleName}, role)
	return role, runtimeclient.IgnoreNotFound(err)
}
