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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"

	"kubesphere.io/kubesphere/pkg/server/errors"
)

func (h *appHandler) CreateOrUpdateCategory(req *restful.Request, resp *restful.Response) {
	createCategoryRequest := &appv2.Category{}
	err := req.ReadEntity(createCategoryRequest)
	if requestDone(err, resp) {
		return
	}

	category := &appv2.Category{}
	category.Name = createCategoryRequest.Name
	MutateFn := func() error {
		if category.GetAnnotations() == nil {
			category.SetAnnotations(map[string]string{})
		}
		if createCategoryRequest.GetAnnotations() != nil &&
			createCategoryRequest.GetAnnotations()[constants.DisplayNameAnnotationKey] != "" {
			category.Annotations[constants.DisplayNameAnnotationKey] =
				createCategoryRequest.GetAnnotations()[constants.DisplayNameAnnotationKey]
		}

		return nil
	}
	_, err = controllerutil.CreateOrUpdate(req.Request.Context(), h.client, category, MutateFn)
	if requestDone(err, resp) {
		return
	}

	resp.WriteAsJson(category)
}

func (h *appHandler) DeleteCategory(req *restful.Request, resp *restful.Response) {
	categoryId := req.PathParameter("category")

	err := h.client.Delete(req.Request.Context(), &appv2.Category{ObjectMeta: metav1.ObjectMeta{Name: categoryId}})
	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeCategory(req *restful.Request, resp *restful.Response) {
	categoryId := req.PathParameter("category")

	result := &appv2.Category{}
	err := h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{Name: categoryId}, result)
	if requestDone(err, resp) {
		return
	}
	result.SetManagedFields(nil)

	resp.WriteEntity(result)
}

func (h *appHandler) ListCategories(req *restful.Request, resp *restful.Response) {
	cList := &appv2.CategoryList{}
	err := h.client.List(req.Request.Context(), cList)
	if requestDone(err, resp) {
		return
	}
	resp.WriteEntity(convertToListResult(cList, req))
}
