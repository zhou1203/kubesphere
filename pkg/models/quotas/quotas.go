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

package quotas

import (
	"context"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/api"
)

const (
	podsKey                   = "count/pods"
	daemonsetsKey             = "count/daemonsets.apps"
	deploymentsKey            = "count/deployments.apps"
	ingressKey                = "count/ingresses.extensions"
	servicesKey               = "count/services"
	statefulsetsKey           = "count/statefulsets.apps"
	persistentvolumeclaimsKey = "persistentvolumeclaims"
	jobsKey                   = "count/jobs.batch"
	cronJobsKey               = "count/cronjobs.batch"
)

var supportedResources = map[string]schema.GroupVersionKind{
	deploymentsKey:            {Group: "apps", Version: "v1", Kind: "Deployment"},
	daemonsetsKey:             {Group: "apps", Version: "v1", Kind: "DaemonSet"},
	statefulsetsKey:           {Group: "apps", Version: "v1", Kind: "StatefulSet"},
	podsKey:                   {Group: "", Version: "v1", Kind: "Pod"},
	servicesKey:               {Group: "", Version: "v1", Kind: "Service"},
	persistentvolumeclaimsKey: {Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
	ingressKey:                {Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
	jobsKey:                   {Group: "batch", Version: "v1", Kind: "Job"},
	cronJobsKey:               {Group: "batch", Version: "v1beta1", Kind: "CronJob"},
}

type ResourceQuotaGetter interface {
	GetClusterQuota() (*api.ResourceQuota, error)
	GetNamespaceQuota(namespace string) (*api.NamespacedResourceQuota, error)
}

type resourceQuotaGetter struct {
	cache runtimeclient.Reader
}

func NewResourceQuotaGetter(cacheReader runtimeclient.Reader) ResourceQuotaGetter {
	return &resourceQuotaGetter{cache: cacheReader}
}

func (c *resourceQuotaGetter) getUsage(namespace, resource string) (int, error) {
	// TODO refactor
	return 0, nil
}

// no one use this api anymore， marked as deprecated
func (c *resourceQuotaGetter) GetClusterQuota() (*api.ResourceQuota, error) {

	quota := v1.ResourceQuotaStatus{Hard: make(v1.ResourceList), Used: make(v1.ResourceList)}

	for r := range supportedResources {
		used, err := c.getUsage("", r)
		if err != nil {
			return nil, err
		}
		var quantity resource.Quantity
		quantity.Set(int64(used))
		quota.Used[v1.ResourceName(r)] = quantity
	}

	return &api.ResourceQuota{Namespace: "\"\"", Data: quota}, nil

}

func (c *resourceQuotaGetter) GetNamespaceQuota(namespace string) (*api.NamespacedResourceQuota, error) {
	quota, err := c.getNamespaceResourceQuota(namespace)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	if quota == nil {
		quota = &v1.ResourceQuotaStatus{Hard: make(v1.ResourceList), Used: make(v1.ResourceList)}
	}

	var resourceQuotaLeft = v1.ResourceList{}

	for key, hardLimit := range quota.Hard {
		if used, ok := quota.Used[key]; ok {
			left := hardLimit.DeepCopy()
			left.Sub(used)
			if hardLimit.Cmp(used) < 0 {
				left = resource.MustParse("0")
			}

			resourceQuotaLeft[key] = left
		}
	}

	// add extra quota usage, cause user may not specify them
	for key := range supportedResources {
		// only add them when they don't exist in quotastatus
		if _, ok := quota.Used[v1.ResourceName(key)]; !ok {
			used, err := c.getUsage(namespace, key)
			if err != nil {
				klog.Error(err)
				return nil, err
			}

			quota.Used[v1.ResourceName(key)] = *(resource.NewQuantity(int64(used), resource.DecimalSI))
		}
	}

	var result = api.NamespacedResourceQuota{
		Namespace: namespace,
	}
	result.Data.Hard = quota.Hard
	result.Data.Used = quota.Used
	result.Data.Left = resourceQuotaLeft

	return &result, nil

}

func updateNamespaceQuota(tmpResourceList, resourceList v1.ResourceList) {
	if tmpResourceList == nil {
		tmpResourceList = resourceList
	}
	for res, usage := range resourceList {
		tmpUsage, exist := tmpResourceList[res]
		if !exist {
			tmpResourceList[res] = usage
		}
		if tmpUsage.Cmp(usage) == 1 {
			tmpResourceList[res] = usage
		}
	}
}

func (c *resourceQuotaGetter) getNamespaceResourceQuota(namespace string) (*v1.ResourceQuotaStatus, error) {
	resourceQuotaList := &v1.ResourceQuotaList{}
	if err := c.cache.List(context.Background(), resourceQuotaList, runtimeclient.InNamespace(namespace)); err != nil {
		klog.Error(err)
		return nil, err
	} else if len(resourceQuotaList.Items) == 0 {
		return nil, nil
	}

	quotaStatus := v1.ResourceQuotaStatus{Hard: make(v1.ResourceList), Used: make(v1.ResourceList)}

	for _, quota := range resourceQuotaList.Items {
		updateNamespaceQuota(quotaStatus.Hard, quota.Status.Hard)
		updateNamespaceQuota(quotaStatus.Used, quota.Status.Used)
	}

	return &quotaStatus, nil
}
