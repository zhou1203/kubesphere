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

package apiserver

import (
	"bytes"
	"context"
	"fmt"

	"net/http"
	rt "runtime"
	"strconv"
	"sync"
	"time"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
	urlruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	unionauth "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/multicluster"

	audit "kubesphere.io/kubesphere/pkg/apiserver/auditing"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/authenticators/basic"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/authenticators/jwt"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/request/anonymous"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/request/basictoken"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/request/bearertoken"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizerfactory"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/path"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/rbac"
	unionauthorizer "kubesphere.io/kubesphere/pkg/apiserver/authorization/union"
	apiserverconfig "kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/apiserver/filters"
	"kubesphere.io/kubesphere/pkg/apiserver/metrics"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	clusterkapisv1alpha1 "kubesphere.io/kubesphere/pkg/kapis/cluster/v1alpha1"
	configv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/config/v1alpha2"
	gatewayv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/gateway/v1alpha2"
	iamapiv1beta1 "kubesphere.io/kubesphere/pkg/kapis/iam/v1beta1"
	"kubesphere.io/kubesphere/pkg/kapis/marketplace"
	"kubesphere.io/kubesphere/pkg/kapis/oauth"
	operationsv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/operations/v1alpha2"
	packagev1alpha1 "kubesphere.io/kubesphere/pkg/kapis/package/v1alpha1"
	resourcesv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha2"
	resourcev1alpha3 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha3"
	tenantv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha2"
	tenantv1alpha3 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha3"
	terminalv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/terminal/v1alpha2"
	"kubesphere.io/kubesphere/pkg/kapis/version"
	"kubesphere.io/kubesphere/pkg/models/auth"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
	"kubesphere.io/kubesphere/pkg/server/healthz"
	"kubesphere.io/kubesphere/pkg/simple/client/auditing"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	overviewclient "kubesphere.io/kubesphere/pkg/simple/client/overview"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/iputil"
)

var initMetrics sync.Once

type APIServer struct {
	// number of kubesphere apiserver
	ServerCount int

	Server *http.Server

	Config *apiserverconfig.Config

	// webservice container, where all webservice defines
	container *restful.Container

	// kubeClient is a collection of all kubernetes(include CRDs) objects clientset
	KubernetesClient k8s.Client

	// cache is used for short-lived objects, like session
	CacheClient cache.Interface

	AuditingClient auditing.Client

	// controller-runtime cache
	RuntimeCache runtimecache.Cache

	// entity that issues tokens
	Issuer token.Issuer

	// controller-runtime client with informer cache
	RuntimeClient runtimeclient.Client

	ClusterClient clusterclient.Interface

	ResourceManager resourcev1beta1.ResourceManager
}

func (s *APIServer) PrepareRun(stopCh <-chan struct{}) error {
	s.container = restful.NewContainer()
	s.container.Filter(logRequestAndResponse)
	s.container.Filter(monitorRequest)
	s.container.Router(restful.CurlyRouter{})
	s.container.RecoverHandler(func(panicReason interface{}, httpWriter http.ResponseWriter) {
		logStackOnRecover(panicReason, httpWriter)
	})
	s.installDynamicResourceAPI()
	s.installKubeSphereAPIs()
	s.installMetricsAPI()
	s.installHealthz()

	for _, ws := range s.container.RegisteredWebServices() {
		klog.V(2).Infof("%s", ws.RootPath())
	}

	s.Server.Handler = s.container

	if err := s.buildHandlerChain(stopCh); err != nil {
		return fmt.Errorf("failed to build handler chain: %v", err)
	}

	return nil
}

func monitorRequest(r *restful.Request, response *restful.Response, chain *restful.FilterChain) {
	start := time.Now()
	chain.ProcessFilter(r, response)
	reqInfo, exists := request.RequestInfoFrom(r.Request.Context())
	if exists && reqInfo.APIGroup != "" {
		RequestCounter.WithLabelValues(reqInfo.Verb, reqInfo.APIGroup, reqInfo.APIVersion, reqInfo.Resource, strconv.Itoa(response.StatusCode())).Inc()
		elapsedSeconds := time.Since(start).Seconds()
		RequestLatencies.WithLabelValues(reqInfo.Verb, reqInfo.APIGroup, reqInfo.APIVersion, reqInfo.Resource).Observe(elapsedSeconds)
	}
}

func (s *APIServer) installMetricsAPI() {
	initMetrics.Do(registerMetrics)
	metrics.Install(s.container)
}

// Install all kubesphere api groups
// Installation happens before all informers start to cache objects,
// so any attempt to list objects using listers will get empty results.
func (s *APIServer) installKubeSphereAPIs() {
	imOperator := im.NewOperator(s.RuntimeClient, s.Config.AuthenticationOptions)
	amOperator := am.NewOperator(s.ResourceManager)
	rbacAuthorizer := rbac.NewRBACAuthorizer(amOperator)

	counter := overviewclient.New(s.RuntimeClient)
	counter.RegisterResource(overviewclient.NewDefaultRegisterOptions()...)
	urlruntime.Must(configv1alpha2.AddToContainer(s.container, s.Config, s.RuntimeClient))
	urlruntime.Must(resourcev1alpha3.AddToContainer(s.container, s.RuntimeCache, counter))
	urlruntime.Must(operationsv1alpha2.AddToContainer(s.container, s.RuntimeClient))
	urlruntime.Must(resourcesv1alpha2.AddToContainer(s.container, s.RuntimeClient,
		s.KubernetesClient.Master(), s.Config.AuthenticationOptions.KubectlImage))
	urlruntime.Must(tenantv1alpha2.AddToContainer(s.container, s.RuntimeClient,
		s.AuditingClient, s.ClusterClient, amOperator, imOperator, rbacAuthorizer, counter))
	urlruntime.Must(tenantv1alpha3.AddToContainer(s.container, s.RuntimeClient,
		s.AuditingClient, s.ClusterClient, amOperator, imOperator, rbacAuthorizer))
	urlruntime.Must(terminalv1alpha2.AddToContainer(s.container, s.KubernetesClient.Kubernetes(),
		rbacAuthorizer, s.KubernetesClient.Config(), s.Config.TerminalOptions))
	urlruntime.Must(clusterkapisv1alpha1.AddToContainer(s.container, s.RuntimeClient))
	urlruntime.Must(iamapiv1beta1.AddToContainer(s.container, imOperator, amOperator))

	urlruntime.Must(oauth.AddToContainer(s.container, imOperator,
		auth.NewTokenOperator(s.CacheClient, s.Issuer, s.Config.AuthenticationOptions),
		auth.NewPasswordAuthenticator(s.RuntimeClient, s.Config.AuthenticationOptions),
		auth.NewOAuthAuthenticator(s.RuntimeClient, s.Config.AuthenticationOptions),
		auth.NewLoginRecorder(s.RuntimeClient), s.Config.AuthenticationOptions))
	urlruntime.Must(version.AddToContainer(s.container, s.KubernetesClient.Kubernetes().Discovery()))
	urlruntime.Must(packagev1alpha1.AddToContainer(s.container, s.RuntimeCache))
	urlruntime.Must(gatewayv1alpha2.AddToContainer(s.container, s.RuntimeCache))

	if s.Config.MultiClusterOptions.ClusterRole == multicluster.ClusterRoleHost {
		urlruntime.Must(marketplace.AddToContainer(s.container, s.RuntimeClient))
	}
}

// installHealthz creates the healthz endpoint for this server
func (s *APIServer) installHealthz() {
	urlruntime.Must(healthz.InstallHandler(s.container, []healthz.HealthChecker{}...))
}

func (s *APIServer) Run(ctx context.Context) (err error) {
	go s.RuntimeCache.Start(ctx)

	shutdownCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = s.Server.Shutdown(shutdownCtx)
	}()

	klog.V(0).Infof("Start listening on %s", s.Server.Addr)
	if s.Server.TLSConfig != nil {
		err = s.Server.ListenAndServeTLS("", "")
	} else {
		err = s.Server.ListenAndServe()
	}

	return err
}

func (s *APIServer) buildHandlerChain(stopCh <-chan struct{}) error {
	requestInfoResolver := &request.RequestInfoFactory{
		APIPrefixes:          sets.New("api", "apis", "kapis", "kapi"),
		GrouplessAPIPrefixes: sets.New("api", "kapi"),
		GlobalResources: []schema.GroupResource{
			iamv1beta1.Resource(iamv1beta1.ResourcesPluralUser),
			iamv1beta1.Resource(iamv1beta1.ResourcesPluralGlobalRole),
			iamv1beta1.Resource(iamv1beta1.ResourcesPluralGlobalRoleBinding),
			tenantv1alpha1.Resource(tenantv1alpha1.ResourcePluralWorkspace),
			tenantv1alpha2.Resource(tenantv1alpha1.ResourcePluralWorkspace),
			tenantv1alpha2.Resource(clusterv1alpha1.ResourcesPluralCluster),
			clusterv1alpha1.Resource(clusterv1alpha1.ResourcesPluralCluster),
			resourcev1alpha3.Resource(clusterv1alpha1.ResourcesPluralCluster),
		},
	}

	handler := s.Server.Handler
	handler = filters.WithKubeAPIServer(handler, s.KubernetesClient.Config())
	handler = filters.WithAPIService(handler, s.RuntimeCache)
	handler = filters.WithReverseProxy(handler, s.RuntimeCache)
	handler = filters.WithJSBundle(handler, s.RuntimeCache)

	if s.Config.AuditingOptions.Enable {
		handler = filters.WithAuditing(handler, audit.NewAuditing(s.RuntimeCache, s.Config.AuditingOptions, stopCh))
	}

	var authorizers authorizer.Authorizer
	switch s.Config.AuthorizationOptions.Mode {
	case authorization.AlwaysAllow:
		authorizers = authorizerfactory.NewAlwaysAllowAuthorizer()
	case authorization.AlwaysDeny:
		authorizers = authorizerfactory.NewAlwaysDenyAuthorizer()
	default:
		fallthrough
	case authorization.RBAC:
		excludedPaths := []string{"/oauth/*", "/kapis/config.kubesphere.io/*", "/kapis/version", "/version", "/metrics", "/healthz"}
		pathAuthorizer, _ := path.NewAuthorizer(excludedPaths)
		amOperator := am.NewReadOnlyOperator(s.ResourceManager)
		authorizers = unionauthorizer.New(pathAuthorizer, rbac.NewRBACAuthorizer(amOperator))
	}

	handler = filters.WithAuthorization(handler, authorizers)
	handler = filters.WithMulticluster(handler, s.ClusterClient)

	// authenticators are unordered
	authn := unionauth.New(anonymous.NewAuthenticator(),
		basictoken.New(basic.NewBasicAuthenticator(
			auth.NewPasswordAuthenticator(s.RuntimeClient, s.Config.AuthenticationOptions),
			auth.NewLoginRecorder(s.RuntimeClient))),
		bearertoken.New(jwt.NewTokenAuthenticator(s.RuntimeCache,
			auth.NewTokenOperator(s.CacheClient, s.Issuer, s.Config.AuthenticationOptions))))
	handler = filters.WithAuthentication(handler, authn)
	handler = filters.WithRequestInfo(handler, requestInfoResolver)
	s.Server.Handler = handler
	return nil
}

func (s *APIServer) installDynamicResourceAPI() {
	dynamicResourceHandler := filters.NewDynamicResourceHandle(func(err restful.ServiceError, req *restful.Request, resp *restful.Response) {
		for header, values := range err.Header {
			for _, value := range values {
				resp.Header().Add(header, value)
			}
		}
		resp.WriteErrorString(err.Code, err.Message)
	}, resourcev1beta1.New(s.RuntimeClient))
	s.container.ServiceErrorHandler(dynamicResourceHandler.HandleServiceError)
}

func logStackOnRecover(panicReason interface{}, w http.ResponseWriter) {
	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("recover from panic situation: - %v\r\n", panicReason))
	for i := 2; ; i += 1 {
		_, file, line, ok := rt.Caller(i)
		if !ok {
			break
		}
		buffer.WriteString(fmt.Sprintf("    %s:%d\r\n", file, line))
	}
	klog.Errorln(buffer.String())

	headers := http.Header{}
	if ct := w.Header().Get("Content-Type"); len(ct) > 0 {
		headers.Set("Accept", ct)
	}

	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte("Internal server error"))
}

func logRequestAndResponse(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	start := time.Now()
	chain.ProcessFilter(req, resp)

	// Always log error response
	logWithVerbose := klog.V(4)
	if resp.StatusCode() > http.StatusBadRequest {
		logWithVerbose = klog.V(0)
	}

	logWithVerbose.Infof("%s - \"%s %s %s\" %d %d %dms",
		iputil.RemoteIp(req.Request),
		req.Request.Method,
		req.Request.URL,
		req.Request.Proto,
		resp.StatusCode(),
		resp.ContentLength(),
		time.Since(start)/time.Millisecond,
	)
}
