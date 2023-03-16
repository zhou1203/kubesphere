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

package filters

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"

	"kubesphere.io/kubesphere/pkg/apiserver/request"
)

type jsBundle struct {
	next  http.Handler
	cache cache.Cache
}

func WithJSBundle(next http.Handler, cache cache.Cache) http.Handler {
	return &jsBundle{next: next, cache: cache}
}

func (s *jsBundle) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	requestInfo, _ := request.RequestInfoFrom(req.Context())

	if requestInfo.IsResourceRequest || requestInfo.IsKubernetesRequest {
		s.next.ServeHTTP(w, req)
		return
	}
	if !strings.HasPrefix(requestInfo.Path, extensionsv1alpha1.DistPrefix) {
		s.next.ServeHTTP(w, req)
		return
	}
	var jsBundles extensionsv1alpha1.JSBundleList
	if err := s.cache.List(req.Context(), &jsBundles, &client.ListOptions{}); err != nil {
		message := fmt.Errorf("failed to list js bundles")
		klog.Errorf("%v: %v", message, err)
		responsewriters.InternalError(w, req, message)
		return
	}
	for _, jsBundle := range jsBundles.Items {
		if jsBundle.Status.State == extensionsv1alpha1.StateAvailable && jsBundle.Status.Link == requestInfo.Path {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			if jsBundle.Spec.Raw != nil {
				s.rawContent(jsBundle.Spec.Raw, w, req)
				return
			}
			if jsBundle.Spec.RawFrom.ConfigMapKeyRef != nil {
				s.rawFromConfigMap(jsBundle.Spec.RawFrom.ConfigMapKeyRef, w, req)
				return
			}
			if jsBundle.Spec.RawFrom.SecretKeyRef != nil {
				s.rawFromSecret(jsBundle.Spec.RawFrom.SecretKeyRef, w, req)
				return
			}
			rawURL := jsBundle.Spec.RawFrom.RawURL()
			if rawURL != "" {
				s.rawFromRemote(rawURL, w, req)
				return
			}
		}
	}
	s.next.ServeHTTP(w, req)
}

func (s *jsBundle) rawFromRemote(rawURL string, w http.ResponseWriter, req *http.Request) {
	location, err := url.Parse(rawURL)
	if err != nil {
		message := "failed to fetch content"
		klog.Errorf("%v: %v", message, err)
		responsewriters.WriteRawJSON(http.StatusServiceUnavailable, message, w)
		return
	}
	handler := proxy.NewUpgradeAwareHandler(location, http.DefaultTransport, false, false, &responder{})
	handler.ServeHTTP(w, req)
}

func (s *jsBundle) rawContent(base64EncodedData []byte, w http.ResponseWriter, req *http.Request) {
	dec := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(base64EncodedData))
	if _, err := io.Copy(w, dec); err != nil {
		message := fmt.Errorf("failed to decode raw content")
		klog.Errorf("%v: %v", message, err)
		responsewriters.InternalError(w, req, message)
	}
}

func (s *jsBundle) rawFromConfigMap(configMapRef *extensionsv1alpha1.ConfigMapKeyRef, w http.ResponseWriter, req *http.Request) {
	var cm v1.ConfigMap
	ref := types.NamespacedName{
		Namespace: configMapRef.Namespace,
		Name:      configMapRef.Name,
	}
	if err := s.cache.Get(req.Context(), ref, &cm); err != nil {
		message := fmt.Errorf("failed to fetch content from configMap, ref: %s", ref)
		klog.Errorf("%v: %v", message, err)
		responsewriters.InternalError(w, req, message)
		return
	}
	w.Write([]byte(cm.Data[configMapRef.Key]))
}

func (s *jsBundle) rawFromSecret(secretRef *extensionsv1alpha1.SecretKeyRef, w http.ResponseWriter, req *http.Request) {
	var secret v1.Secret
	ref := types.NamespacedName{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}
	if err := s.cache.Get(req.Context(), ref, &secret); err != nil {
		message := fmt.Errorf("failed to fetch content from secret, ref: %s", ref)
		klog.Errorf("%v: %v", message, err)
		responsewriters.InternalError(w, req, err)
		return
	}
	w.Write(secret.Data[secretRef.Key])
}
