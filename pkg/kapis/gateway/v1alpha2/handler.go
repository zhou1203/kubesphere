/*
Copyright 2023 KubeSphere Authors

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
	"context"

	"github.com/emicklei/go-restful/v3"
	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/api/gateway/v1alpha2"

	"kubesphere.io/kubesphere/pkg/api"
)

const (
	MasterLabel       = "node-role.kubernetes.io/control-plane"
	SvcNameAnnotation = "gateway.kubesphere.io/service-name"
)

type handler struct {
	cache runtimeclient.Reader
}

func newHandler(cache runtimeclient.Reader) *handler {
	return &handler{
		cache: cache,
	}
}

func (h *handler) ingressClassScopeList(request *restful.Request, response *restful.Response) {
	currentNs := request.PathParameter("namespace")

	ingressClassScopeList := v1alpha2.IngressClassScopeList{}
	err := h.cache.List(context.TODO(), &ingressClassScopeList)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	var ret []v1alpha2.IngressClassScope
	for _, item := range ingressClassScopeList.Items {
		namespaces := item.Spec.Scope.Namespaces
		nsSelector := item.Spec.Scope.NamespaceSelector

		// Specify all namespace
		if len(namespaces) == 0 && nsSelector == "" {
			_ = h.setStatus(&item)
			ret = append(ret, item)
			continue
		}

		// Specify namespaces
		if len(namespaces) > 0 {
			for _, n := range namespaces {
				if n == currentNs {
					_ = h.setStatus(&item)
					ret = append(ret, item)
					break
				}
			}
			continue
		}

		// Specify namespaceSelector
		if nsSelector != "" {
			nsList := corev1.NamespaceList{}
			_ = h.cache.List(context.TODO(), &nsList, &runtimeclient.ListOptions{LabelSelector: Selector(nsSelector)})
			for _, n := range nsList.Items {
				if n.Name == currentNs {
					_ = h.setStatus(&item)
					ret = append(ret, item)
					break
				}
			}
		}
	}

	response.WriteEntity(ret)
}

func Selector(s string) labels.Selector {
	if selector, err := labels.Parse(s); err != nil {
		return labels.Everything()
	} else {
		return selector
	}
}

func (h *handler) getMasterNodeIp() []string {
	internalIps := []string{}
	masters := &corev1.NodeList{}
	err := h.cache.List(context.TODO(), masters, &runtimeclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(
			labels.Set{
				MasterLabel: "",
			})})

	if err != nil {
		klog.Info(err)
		return internalIps
	}

	for _, node := range masters.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				internalIps = append(internalIps, address.Address)
			}
		}
	}
	return internalIps
}

func (h *handler) setStatus(ics *v1alpha2.IngressClassScope) (e error) {
	if ics.Annotations[SvcNameAnnotation] == "" {
		klog.Errorf("namespace: %s, name: %s, No %s annotation", ics.Namespace, ics.Name, SvcNameAnnotation)
		return nil
	}

	svc := corev1.Service{}
	key := types.NamespacedName{
		Namespace: ics.Namespace,
		Name:      ics.Annotations[SvcNameAnnotation],
	}

	if err := h.cache.Get(context.TODO(), key, &svc); err != nil {
		klog.Errorf("Failed to fetch svc name: %v, %v", ics.Name, err)
		return err
	}

	// append selected node ip as loadBalancer ingress ip
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer && len(svc.Status.LoadBalancer.Ingress) == 0 {
		rips := h.getMasterNodeIp()
		for _, rip := range rips {
			gIngress := corev1.LoadBalancerIngress{
				IP: rip,
			}
			svc.Status.LoadBalancer.Ingress = append(svc.Status.LoadBalancer.Ingress, gIngress)
		}
	}

	status := unstructured.Unstructured{
		Object: map[string]interface{}{
			"loadBalancer": svc.Status.LoadBalancer,
			"service":      svc.Spec.Ports,
		},
	}

	target, err := status.MarshalJSON()
	if err != nil {
		return err
	}

	if ics.Status.Raw != nil {
		//merge with origin status
		patch, err := jsonpatch.CreateMergePatch([]byte(`{}`), target)
		if err != nil {
			return err
		}
		modified, err := jsonpatch.MergePatch(ics.Status.Raw, patch)
		if err != nil {
			return err
		}
		ics.Status.Raw = modified
	}
	ics.Status.Raw = target
	return nil
}
