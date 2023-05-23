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
	"bytes"
	"net/http"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/emicklei/go-restful/v3"
	"helm.sh/helm/v3/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/api"
)

type handler struct {
	cache runtimeclient.Reader
}

func newHandler(cache runtimeclient.Reader) *handler {
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
	if extensionVersion.Spec.ChartDataRef != nil {
		configMap := &corev1.ConfigMap{}
		if err := h.cache.Get(request.Request.Context(), types.NamespacedName{Namespace: extensionVersion.Spec.ChartDataRef.Namespace, Name: extensionVersion.Spec.ChartDataRef.Name}, configMap); err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		data := configMap.BinaryData[extensionVersion.Spec.ChartDataRef.Key]
		if data == nil {
			response.WriteEntity([]interface{}{})
			return
		}
		files, err := loader.LoadArchiveFiles(bytes.NewReader(data))
		if err != nil {
			api.HandleInternalError(response, request, err)
			return
		}
		response.WriteEntity(files)
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
