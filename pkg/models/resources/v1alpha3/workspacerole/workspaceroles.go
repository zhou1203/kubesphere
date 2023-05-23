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

package workspacerole

import (
	"context"
	"encoding/json"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
)

type workspacerolesGetter struct {
	cache runtimeclient.Reader
}

func New(cache runtimeclient.Reader) v1alpha3.Interface {
	return &workspacerolesGetter{cache: cache}
}

func (d *workspacerolesGetter) Get(_, name string) (runtime.Object, error) {
	globalRole := &apiextensionsv1.CustomResourceDefinition{}
	return globalRole, d.cache.Get(context.Background(), types.NamespacedName{Name: name}, globalRole)
}

func (d *workspacerolesGetter) List(_ string, query *query.Query) (*api.ListResult, error) {

	var roles []*iamv1beta1.WorkspaceRole
	var err error
	if aggregateTo := query.Filters[iamv1beta1.AggregateTo]; aggregateTo != "" {
		roles, err = d.fetchAggregationRoles(string(aggregateTo))
		if err != nil {
			return nil, err
		}
		delete(query.Filters, iamv1beta1.AggregateTo)
	} else {
		workspaceRoleList := &iamv1beta1.WorkspaceRoleList{}
		if err = d.cache.List(context.Background(), workspaceRoleList,
			client.MatchingLabelsSelector{Selector: query.Selector()}); err != nil {
			return nil, err
		}
		roles = make([]*iamv1beta1.WorkspaceRole, 0)
		for _, item := range workspaceRoleList.Items {
			roles = append(roles, item.DeepCopy())
		}
	}

	var result []runtime.Object
	for _, role := range roles {
		result = append(result, role)
	}

	return v1alpha3.DefaultList(result, query, d.compare, d.filter), nil
}

func (d *workspacerolesGetter) compare(left runtime.Object, right runtime.Object, field query.Field) bool {

	leftRole, ok := left.(*iamv1beta1.WorkspaceRole)
	if !ok {
		return false
	}

	rightRole, ok := right.(*iamv1beta1.WorkspaceRole)
	if !ok {
		return false
	}

	return v1alpha3.DefaultObjectMetaCompare(leftRole.ObjectMeta, rightRole.ObjectMeta, field)
}

func (d *workspacerolesGetter) filter(object runtime.Object, filter query.Filter) bool {
	role, ok := object.(*iamv1beta1.WorkspaceRole)

	if !ok {
		return false
	}

	switch filter.Field {
	case iamv1beta1.ScopeWorkspace:
		return role.Labels[tenantv1alpha1.WorkspaceLabel] == string(filter.Value)
	default:
		return v1alpha3.DefaultObjectMetaFilter(role.ObjectMeta, filter)
	}

}

func (d *workspacerolesGetter) fetchAggregationRoles(name string) ([]*iamv1beta1.WorkspaceRole, error) {
	roles := make([]*iamv1beta1.WorkspaceRole, 0)

	obj, err := d.Get("", name)

	if err != nil {
		if errors.IsNotFound(err) {
			return roles, nil
		}
		return nil, err
	}

	if annotation := obj.(*iamv1beta1.WorkspaceRole).Annotations[iamv1beta1.AggregationRolesAnnotation]; annotation != "" {
		var roleNames []string
		if err = json.Unmarshal([]byte(annotation), &roleNames); err == nil {

			for _, roleName := range roleNames {
				role, err := d.Get("", roleName)

				if err != nil {
					if errors.IsNotFound(err) {
						klog.Warningf("invalid aggregation role found: %s, %s", name, roleName)
						continue
					}
					return nil, err
				}

				roles = append(roles, role.(*iamv1beta1.WorkspaceRole))
			}
		}
	}

	return roles, nil
}
