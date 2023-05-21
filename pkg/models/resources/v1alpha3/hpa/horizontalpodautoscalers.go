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
package hpa

import (
	"context"

	autoscalingv2beta2 "k8s.io/api/autoscaling/v2beta2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
)

type hpaGetter struct {
	cache runtimeclient.Reader
}

func New(cache runtimeclient.Reader) v1alpha3.Interface {
	return &hpaGetter{cache: cache}
}

func (s *hpaGetter) Get(namespace, name string) (runtime.Object, error) {
	hpa := &autoscalingv2beta2.HorizontalPodAutoscaler{}
	return hpa, s.cache.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, hpa)
}

func (s *hpaGetter) List(namespace string, query *query.Query) (*api.ListResult, error) {
	hpaList := &autoscalingv2beta2.HorizontalPodAutoscalerList{}
	if err := s.cache.List(context.Background(), hpaList, client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: query.Selector()}); err != nil {
		return nil, err
	}
	var result []runtime.Object
	for _, item := range hpaList.Items {
		result = append(result, item.DeepCopy())
	}
	return v1alpha3.DefaultList(result, query, s.compare, s.filter), nil
}

func (s *hpaGetter) compare(left runtime.Object, right runtime.Object, field query.Field) bool {

	leftHPA, ok := left.(*autoscalingv2beta2.HorizontalPodAutoscaler)
	if !ok {
		return false
	}

	rightHPA, ok := right.(*autoscalingv2beta2.HorizontalPodAutoscaler)
	if !ok {
		return false
	}

	return v1alpha3.DefaultObjectMetaCompare(leftHPA.ObjectMeta, rightHPA.ObjectMeta, field)
}

func (s *hpaGetter) filter(object runtime.Object, filter query.Filter) bool {
	hpa, ok := object.(*autoscalingv2beta2.HorizontalPodAutoscaler)
	if !ok {
		return false
	}

	switch filter.Field {
	case "targetKind":
		return hpa.Spec.ScaleTargetRef.Name == string(filter.Value)
	case "targetName":
		return hpa.Spec.ScaleTargetRef.Name == string(filter.Value)
	default:
		return v1alpha3.DefaultObjectMetaFilter(hpa.ObjectMeta, filter)
	}
}
