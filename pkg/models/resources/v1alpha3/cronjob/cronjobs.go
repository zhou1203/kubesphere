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

package cronjob

import (
	"context"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
)

type cronJobsGetter struct {
	cache runtimeclient.Reader
}

func New(cache runtimeclient.Reader) v1alpha3.Interface {
	return &cronJobsGetter{cache: cache}
}

func (d *cronJobsGetter) Get(namespace, name string) (runtime.Object, error) {
	cronJob := &batchv1.CronJob{}
	return cronJob, d.cache.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, cronJob)
}

func (d *cronJobsGetter) List(namespace string, query *query.Query) (*api.ListResult, error) {
	jobs := &batchv1.CronJobList{}
	if err := d.cache.List(context.Background(), jobs, client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: query.Selector()}); err != nil {
		return nil, err
	}
	var result []runtime.Object
	for _, item := range jobs.Items {
		result = append(result, item.DeepCopy())
	}
	return v1alpha3.DefaultList(result, query, d.compare, d.filter), nil
}

func cronJobStatus(item *batchv1.CronJob) string {
	if item.Spec.Suspend != nil && *item.Spec.Suspend {
		return "paused"
	}
	return "running"
}

func (d *cronJobsGetter) filter(object runtime.Object, filter query.Filter) bool {
	job, ok := object.(*batchv1.CronJob)
	if !ok {
		return false
	}
	switch filter.Field {
	case query.FieldStatus:
		return strings.Compare(cronJobStatus(job), string(filter.Value)) == 0
	default:
		return v1alpha3.DefaultObjectMetaFilter(job.ObjectMeta, filter)
	}
}

func (d *cronJobsGetter) compare(left runtime.Object, right runtime.Object, field query.Field) bool {
	leftJob, ok := left.(*batchv1.CronJob)
	if !ok {
		return false
	}

	rightJob, ok := right.(*batchv1.CronJob)
	if !ok {
		return false
	}

	return v1alpha3.DefaultObjectMetaCompare(leftJob.ObjectMeta, rightJob.ObjectMeta, field)
}
