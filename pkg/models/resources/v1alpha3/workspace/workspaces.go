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

package workspace

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
)

type workspaceGetter struct {
	cache runtimeclient.Reader
}

func New(cache runtimeclient.Reader) v1alpha3.Interface {
	return &workspaceGetter{cache: cache}
}

func (d *workspaceGetter) Get(_, name string) (runtime.Object, error) {
	workspace := &tenantv1alpha1.Workspace{}
	return workspace, d.cache.Get(context.Background(), types.NamespacedName{Name: name}, workspace)
}

func (d *workspaceGetter) List(_ string, query *query.Query) (*api.ListResult, error) {
	workspaces := &tenantv1alpha1.WorkspaceList{}
	if err := d.cache.List(context.Background(), workspaces,
		client.MatchingLabelsSelector{Selector: query.Selector()}); err != nil {
		return nil, err
	}
	var result []runtime.Object
	for _, item := range workspaces.Items {
		result = append(result, item.DeepCopy())
	}
	return v1alpha3.DefaultList(result, query, d.compare, d.filter), nil
}

func (d *workspaceGetter) compare(left runtime.Object, right runtime.Object, field query.Field) bool {

	leftWorkspace, ok := left.(*tenantv1alpha1.Workspace)
	if !ok {
		return false
	}

	rightWorkspace, ok := right.(*tenantv1alpha1.Workspace)
	if !ok {
		return false
	}

	return v1alpha3.DefaultObjectMetaCompare(leftWorkspace.ObjectMeta, rightWorkspace.ObjectMeta, field)
}

func (d *workspaceGetter) filter(object runtime.Object, filter query.Filter) bool {
	role, ok := object.(*tenantv1alpha1.Workspace)

	if !ok {
		return false
	}

	return v1alpha3.DefaultObjectMetaFilter(role.ObjectMeta, filter)
}
