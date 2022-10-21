/*
Copyright 2021 KubeSphere Authors

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

package v1alpha1

import (
	"net/http"

	"github.com/emicklei/go-restful"
	"helm.sh/helm/v3/pkg/chart/loader"
	"k8s.io/apimachinery/pkg/types"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"kubesphere.io/kubesphere/pkg/api"
)

type handler struct {
	cache cache.Cache
}

func newHandler(cache cache.Cache) *handler {
	return &handler{
		cache: cache,
	}
}

func (h *handler) handleFiles(request *restful.Request, response *restful.Response) {
	extensionVersion := corev1alpha1.ExtensionVersion{}
	if err := h.cache.Get(request.Request.Context(), types.NamespacedName{Name: request.PathParameter("version")}, &extensionVersion); err != nil {
		api.HandleError(response, request, err)
		return
	}
	// TODO load files from chart data
	if extensionVersion.Spec.ChartURL == "" {
		response.WriteEntity([]interface{}{})
		return
	}
	resp, err := http.Get(extensionVersion.Spec.ChartURL)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	defer resp.Body.Close()
	files, err := loader.LoadArchiveFiles(resp.Body)
	if err != nil {
		api.HandleInternalError(response, request, err)
		return
	}
	response.WriteEntity(files)
}
