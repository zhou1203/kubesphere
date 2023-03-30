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

package resource

import (
	"errors"

	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/persistentvolume"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/informers"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/cluster"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/clusterrole"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/clusterrolebinding"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/configmap"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/customresourcedefinition"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/daemonset"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/deployment"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/globalrole"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/globalrolebinding"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/group"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/groupbinding"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/ingress"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/job"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/loginrecord"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/namespace"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/node"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/persistentvolumeclaim"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/pod"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/role"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/rolebinding"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/secret"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/service"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/serviceaccount"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/statefulset"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/user"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/workspace"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/workspacerole"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/workspacerolebinding"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/workspacetemplate"
)

var ErrResourceNotSupported = errors.New("resource is not supported")

type ResourceGetter struct {
	clusterResourceGetters    map[schema.GroupVersionResource]v1alpha3.Interface
	namespacedResourceGetters map[schema.GroupVersionResource]v1alpha3.Interface
}

func NewResourceGetter(factory informers.InformerFactory) *ResourceGetter {
	namespacedResourceGetters := make(map[schema.GroupVersionResource]v1alpha3.Interface)
	clusterResourceGetters := make(map[schema.GroupVersionResource]v1alpha3.Interface)

	namespacedResourceGetters[schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}] = deployment.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}] = daemonset.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}] = statefulset.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}] = service.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}] = configmap.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}] = secret.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}] = pod.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}] = serviceaccount.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}] = ingress.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}] = job.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}] = persistentvolume.New(factory.KubernetesSharedInformerFactory())

	namespacedResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}] = persistentvolumeclaim.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[rbacv1.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralRoleBinding)] = rolebinding.New(factory.KubernetesSharedInformerFactory())
	namespacedResourceGetters[rbacv1.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralRole)] = role.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}] = node.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}] = namespace.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}] = customresourcedefinition.New(factory.ApiExtensionSharedInformerFactory())

	// kubesphere resources
	clusterResourceGetters[tenantv1alpha1.SchemeGroupVersion.WithResource(tenantv1alpha1.ResourcePluralWorkspace)] = workspace.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[tenantv1alpha1.SchemeGroupVersion.WithResource(tenantv1alpha2.ResourcePluralWorkspaceTemplate)] = workspacetemplate.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralGlobalRole)] = globalrole.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralWorkspaceRole)] = workspacerole.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralUser)] = user.New(factory.KubeSphereSharedInformerFactory(), factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralGlobalRoleBinding)] = globalrolebinding.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralWorkspaceRoleBinding)] = workspacerolebinding.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralLoginRecord)] = loginrecord.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcePluralGroup)] = group.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[iamv1alpha2.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcePluralGroupBinding)] = groupbinding.New(factory.KubeSphereSharedInformerFactory())
	clusterResourceGetters[rbacv1.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralClusterRole)] = clusterrole.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[rbacv1.SchemeGroupVersion.WithResource(iamv1alpha2.ResourcesPluralClusterRoleBinding)] = clusterrolebinding.New(factory.KubernetesSharedInformerFactory())
	clusterResourceGetters[clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesPluralCluster)] = cluster.New(factory.KubeSphereSharedInformerFactory())

	return &ResourceGetter{
		namespacedResourceGetters: namespacedResourceGetters,
		clusterResourceGetters:    clusterResourceGetters,
	}
}

// TryResource will retrieve a getter with resource name, it doesn't guarantee find resource with correct group version
// need to refactor this use schema.GroupVersionResource
func (r *ResourceGetter) TryResource(clusterScope bool, resource string) v1alpha3.Interface {
	if clusterScope {
		for k, v := range r.clusterResourceGetters {
			if k.Resource == resource {
				return v
			}
		}
	}
	for k, v := range r.namespacedResourceGetters {
		if k.Resource == resource {
			return v
		}
	}
	return nil
}

func (r *ResourceGetter) Get(resource, namespace, name string) (runtime.Object, error) {
	clusterScope := namespace == ""
	getter := r.TryResource(clusterScope, resource)
	if getter == nil {
		return nil, ErrResourceNotSupported
	}
	return getter.Get(namespace, name)
}

func (r *ResourceGetter) List(resource, namespace string, query *query.Query) (*api.ListResult, error) {
	clusterScope := namespace == ""
	getter := r.TryResource(clusterScope, resource)
	if getter == nil {
		return nil, ErrResourceNotSupported
	}
	return getter.List(namespace, query)
}
