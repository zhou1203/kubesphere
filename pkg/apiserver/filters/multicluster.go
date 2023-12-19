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

package filters

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/httpstream"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/apiserver/request"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const proxyURLFormat = "/api/v1/namespaces/kubesphere-system/services/:ks-apiserver:/proxy%s"

type multiclusterDispatcher struct {
	next http.Handler
	clusterclient.Interface
}

// WithMulticluster forward request to desired cluster based on request cluster name
// which included in request path clusters/{cluster}
func WithMulticluster(next http.Handler, clusterClient clusterclient.Interface) http.Handler {
	if clusterClient == nil {
		klog.V(4).Infof("Multicluster dispatcher is disabled")
		return next
	}
	return &multiclusterDispatcher{
		next:      next,
		Interface: clusterClient,
	}
}

func (m *multiclusterDispatcher) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	info, ok := request.RequestInfoFrom(req.Context())
	if !ok {
		responsewriters.InternalError(w, req, errors.NewInternalError(fmt.Errorf("no RequestInfo found in the context")))
		return
	}
	if info.Cluster == "" {
		m.next.ServeHTTP(w, req)
		return
	}

	cluster, err := m.Get(info.Cluster)
	if err != nil {
		if errors.IsNotFound(err) {
			responsewriters.WriteRawJSON(http.StatusBadRequest, errors.NewBadRequest(fmt.Sprintf("cluster %s not exists", info.Cluster)), w)
		} else {
			responsewriters.InternalError(w, req, err)
		}
		return
	}

	// request cluster is host cluster, no need go through agent
	if clusterutils.IsHostCluster(cluster) {
		req.URL.Path = strings.Replace(req.URL.Path, fmt.Sprintf("/clusters/%s", info.Cluster), "", 1)
		m.next.ServeHTTP(w, req)
		return
	}

	if !clusterutils.IsClusterReady(cluster) {
		responsewriters.WriteRawJSON(http.StatusServiceUnavailable, errors.NewServiceUnavailable(fmt.Sprintf("cluster %s is not ready", cluster.Name)), w)
		return
	}

	clusterClient, err := m.GetClusterClient(cluster.Name)
	if err != nil {
		responsewriters.WriteRawJSON(http.StatusServiceUnavailable, errors.NewServiceUnavailable(err.Error()), w)
		return
	}

	transport := http.DefaultTransport

	location := &url.URL{}
	location.Path = strings.Replace(req.URL.Path, fmt.Sprintf("/clusters/%s", info.Cluster), "", 1)
	location.RawQuery = req.URL.Query().Encode()

	// WithContext creates a shallow clone of the request with the same context.
	newReq := req.WithContext(req.Context())
	newReq.Header = utilnet.CloneHeader(req.Header)
	newReq.URL = location
	newReq.Host = location.Host

	// if cluster connection is direct and kubesphere apiserver endpoint is empty
	// we use kube-apiserver proxy way
	if cluster.Spec.Connection.Type == clusterv1alpha1.ConnectionTypeDirect &&
		len(cluster.Spec.Connection.KubeSphereAPIEndpoint) == 0 {

		location.Scheme = clusterClient.KubernetesURL.Scheme
		location.Host = clusterClient.KubernetesURL.Host
		location.Path = fmt.Sprintf(proxyURLFormat, location.Path)
		transport = clusterClient.Transport

		// The reason we need this is kube-apiserver doesn't behave like a standard proxy, it will strip
		// authorization header of proxy requests. Use custom header to avoid stripping by kube-apiserver.
		// https://github.com/kubernetes/kubernetes/issues/38775#issuecomment-277915961
		// We first copy req.Header['Authorization'] to req.Header['X-KubeSphere-Authorization'] before sending
		// designated cluster kube-apiserver, then copy req.Header['X-KubeSphere-Authorization'] to
		// req.Header['Authorization'] before authentication.
		newReq.Header.Set("X-KubeSphere-Authorization", req.Header.Get("Authorization"))

		// If cluster kubeconfig using token authentication, transport will not override authorization header,
		// this will cause requests reject by kube-apiserver since kubesphere authorization header is not
		// acceptable. Delete this header is safe since we are using X-KubeSphere-Authorization.
		// https://github.com/kubernetes/client-go/blob/master/transport/round_trippers.go#L285
		newReq.Header.Del("Authorization")

		// Dirty trick again. The kube-apiserver apiserver proxy rejects all proxy requests with dryRun parameter
		// https://github.com/kubernetes/kubernetes/pull/66083
		// Really don't understand why they do this. And here we are, bypass with replacing 'dryRun'
		// with dryrun and switch bach before send to kube-apiserver on the other side.
		if len(newReq.URL.Query()["dryRun"]) != 0 {
			newReq.URL.RawQuery = strings.Replace(req.URL.RawQuery, "dryRun", "dryrun", 1)
		}

		// kube-apiserver lost query string when proxy websocket requests, there are several issues opened
		// tracking this, like https://github.com/kubernetes/kubernetes/issues/89360. Also there is a promising
		// PR aim to fix this, but it's unlikely it will get merged soon. So here we are again. Put raw query
		// string in Header and extract it on member cluster.
		if httpstream.IsUpgradeRequest(req) && len(req.URL.RawQuery) != 0 {
			newReq.Header.Set("X-KubeSphere-Rawquery", req.URL.RawQuery)
		}
	} else {
		// everything else goes to ks-apiserver, since our ks-apiserver has the ability to proxy kube-apiserver requests
		location.Scheme = clusterClient.KubeSphereURL.Scheme
		location.Host = clusterClient.KubeSphereURL.Host
	}

	upgrade := httpstream.IsUpgradeRequest(req)
	httpProxy := proxy.NewUpgradeAwareHandler(location, transport, false, upgrade, &responder{})
	httpProxy.UpgradeTransport = proxy.NewUpgradeRequestRoundTripper(transport, transport)
	httpProxy.ServeHTTP(w, newReq)
}
