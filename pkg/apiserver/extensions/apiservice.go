/*

 Copyright 2021 The KubeSphere Authors.

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

package extensions

import (
	"fmt"
	"net/http"
	"net/url"

	"k8s.io/apimachinery/pkg/util/httpstream"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/client-go/transport"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"

	"kubesphere.io/kubesphere/pkg/apiserver/filters"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
)

type apiService struct {
	cache cache.Cache
}

func NewAPIService(cache cache.Cache) filters.Middleware {
	return &apiService{cache: cache}
}

func (s *apiService) Handle(w http.ResponseWriter, req *http.Request) bool {
	requestInfo, _ := request.RequestInfoFrom(req.Context())
	if requestInfo.IsKubernetesRequest {
		return false
	}
	if !requestInfo.IsResourceRequest {
		return false
	}
	var apiServices extensionsv1alpha1.APIServiceList
	if err := s.cache.List(req.Context(), &apiServices); err != nil {
		message := fmt.Errorf("failed to list apiServices")
		klog.Errorf("%v: %v", message, err)
		responsewriters.InternalError(w, req, message)
		return true
	}
	for _, apiService := range apiServices.Items {
		if apiService.Spec.Group != requestInfo.APIGroup || apiService.Spec.Version != requestInfo.APIVersion {
			continue
		}
		if apiService.Status.State != extensionsv1alpha1.StateAvailable {
			message := fmt.Sprintf("apiService %s is not available", apiService.Name)
			klog.Warningf(fmt.Sprintf("%s: current state is %v", message, apiService.Status.State))
			responsewriters.WriteRawJSON(http.StatusServiceUnavailable, message, w)
			return true
		}
		s.handleProxyRequest(apiService, w, req)
		return true
	}
	return false
}

func (s *apiService) handleProxyRequest(apiService extensionsv1alpha1.APIService, w http.ResponseWriter, req *http.Request) {
	endpoint, err := url.Parse(apiService.Spec.RawURL())
	if err != nil {
		klog.Warningf(fmt.Sprintf("apiService %s is not available: %v", apiService.Name, err))
		responsewriters.WriteRawJSON(http.StatusServiceUnavailable, fmt.Sprintf("apiService %s is not available", apiService.Name), w)
		return
	}
	location := &url.URL{}
	location.Scheme = endpoint.Scheme
	location.Host = endpoint.Host
	location.Path = req.URL.Path
	location.RawQuery = req.URL.Query().Encode()

	newReq := req.WithContext(req.Context())
	newReq.Header = utilnet.CloneHeader(req.Header)
	newReq.URL = location
	newReq.Host = location.Host

	user, _ := request.UserFrom(req.Context())
	proxyRoundTripper := transport.NewAuthProxyRoundTripper(user.GetName(), user.GetGroups(), user.GetExtra(), http.DefaultTransport)

	upgrade := httpstream.IsUpgradeRequest(req)
	handler := proxy.NewUpgradeAwareHandler(location, proxyRoundTripper, true, upgrade, s)
	handler.ServeHTTP(w, newReq)
}

func (s *apiService) Error(w http.ResponseWriter, req *http.Request, err error) {
	responsewriters.InternalError(w, req, err)
}
