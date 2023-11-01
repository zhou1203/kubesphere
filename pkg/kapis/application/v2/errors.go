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

package v2

import (
	"github.com/emicklei/go-restful/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	resv1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
)

func handleError(resp *restful.Response, err error) {
	if status.Code(err) == codes.NotFound {
		klog.V(4).Infoln(err)
		api.HandleNotFound(resp, nil, err)
		return
	}
	if status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.FailedPrecondition {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}
	klog.Errorln(err)
	api.HandleInternalError(resp, nil, err)
}
func requestDone(err error, resp *restful.Response) bool {
	if err != nil {
		if apierrors.IsNotFound(err) {
			api.HandleNotFound(resp, nil, err)
			return true
		}
		klog.V(4).Infoln(err)
		api.HandleInternalError(resp, nil, err)
		return true
	}
	return false
}

func convertToListResult(obj runtime.Object, req *restful.Request) (listResult api.ListResult) {
	_ = meta.EachListItem(obj, omitManagedFields)
	queryParams := query.ParseQueryParameter(req)
	list, _ := meta.ExtractList(obj)
	items, _, totalCount := resv1beta1.DefaultList(list, queryParams, resv1beta1.DefaultCompare, resv1beta1.DefaultFilter)

	listResult.Items = items
	listResult.TotalItems = int(*totalCount)

	return listResult
}
func omitManagedFields(o runtime.Object) error {
	a, err := meta.Accessor(o)
	if err != nil {
		return err
	}
	a.SetManagedFields(nil)
	return nil
}
