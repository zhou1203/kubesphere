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

	tenantv1alpha3 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha3"

	"github.com/emicklei/go-restful"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	urlruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	unionauth "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"

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
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/informers"
	clusterkapisv1alpha1 "kubesphere.io/kubesphere/pkg/kapis/cluster/v1alpha1"
	configv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/config/v1alpha2"
	iamapi "kubesphere.io/kubesphere/pkg/kapis/iam/v1alpha2"
	"kubesphere.io/kubesphere/pkg/kapis/oauth"
	operationsv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/operations/v1alpha2"
	packagev1alpha1 "kubesphere.io/kubesphere/pkg/kapis/package/v1alpha1"
	resourcesv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha2"
	resourcev1alpha3 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha3"
	tenantv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha2"
	terminalv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/terminal/v1alpha2"
	"kubesphere.io/kubesphere/pkg/kapis/version"
	"kubesphere.io/kubesphere/pkg/models/auth"
	"kubesphere.io/kubesphere/pkg/models/iam/am"
	"kubesphere.io/kubesphere/pkg/models/iam/group"
	"kubesphere.io/kubesphere/pkg/models/iam/im"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/loginrecord"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/user"
	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
	"kubesphere.io/kubesphere/pkg/server/healthz"
	"kubesphere.io/kubesphere/pkg/simple/client/auditing"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/iputil"
	"kubesphere.io/kubesphere/pkg/utils/metrics"
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

	// informerFactory is a collection of all kubernetes(include CRDs) objects informers,
	// mainly for fast query
	InformerFactory informers.InformerFactory

	// cache is used for short-lived objects, like session
	CacheClient cache.Interface

	AuditingClient auditing.Client

	// controller-runtime cache
	RuntimeCache runtimecache.Cache

	// entity that issues tokens
	Issuer token.Issuer

	// controller-runtime client
	RuntimeClient runtimeclient.Client

	ClusterClient clusterclient.ClusterClients
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
	s.installKubeSphereAPIs(stopCh)
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
	metrics.Defaults.Install(s.container)
}

// Install all kubesphere api groups
// Installation happens before all informers start to cache objects,
// so any attempt to list objects using listers will get empty results.
func (s *APIServer) installKubeSphereAPIs(stopCh <-chan struct{}) {
	imOperator := im.NewOperator(s.KubernetesClient.KubeSphere(),
		user.New(s.InformerFactory.KubeSphereSharedInformerFactory(),
			s.InformerFactory.KubernetesSharedInformerFactory()),
		loginrecord.New(s.InformerFactory.KubeSphereSharedInformerFactory()),
		s.Config.AuthenticationOptions)
	amOperator := am.NewOperator(s.KubernetesClient.KubeSphere(),
		s.KubernetesClient.Kubernetes(),
		s.InformerFactory)
	rbacAuthorizer := rbac.NewRBACAuthorizer(amOperator)

	urlruntime.Must(configv1alpha2.AddToContainer(s.container, s.Config))
	urlruntime.Must(resourcev1alpha3.AddToContainer(s.container, s.InformerFactory))
	urlruntime.Must(operationsv1alpha2.AddToContainer(s.container, s.KubernetesClient.Kubernetes()))
	urlruntime.Must(resourcesv1alpha2.AddToContainer(s.container, s.KubernetesClient.Kubernetes(), s.InformerFactory,
		s.KubernetesClient.Master(), s.Config.AuthenticationOptions.KubectlImage))
	urlruntime.Must(tenantv1alpha2.AddToContainer(s.container, s.InformerFactory, s.KubernetesClient.Kubernetes(),
		s.KubernetesClient.KubeSphere(), s.AuditingClient, amOperator, imOperator, rbacAuthorizer))
	urlruntime.Must(tenantv1alpha3.AddToContainer(s.container, s.InformerFactory, s.KubernetesClient.Kubernetes(),
		s.KubernetesClient.KubeSphere(), s.AuditingClient, amOperator, imOperator, rbacAuthorizer))
	urlruntime.Must(terminalv1alpha2.AddToContainer(s.container, s.KubernetesClient.Kubernetes(), rbacAuthorizer, s.KubernetesClient.Config(), s.Config.TerminalOptions))
	urlruntime.Must(clusterkapisv1alpha1.AddToContainer(s.container,
		s.KubernetesClient.KubeSphere(),
		s.InformerFactory.KubernetesSharedInformerFactory(),
		s.InformerFactory.KubeSphereSharedInformerFactory(),
		s.Config.MultiClusterOptions.ProxyPublishService,
		s.Config.MultiClusterOptions.ProxyPublishAddress,
		s.Config.MultiClusterOptions.AgentImage))
	urlruntime.Must(iamapi.AddToContainer(s.container, imOperator, amOperator,
		group.New(s.InformerFactory, s.KubernetesClient.KubeSphere(), s.KubernetesClient.Kubernetes()),
		rbacAuthorizer))

	userLister := s.InformerFactory.KubeSphereSharedInformerFactory().Iam().V1alpha2().Users().Lister()
	urlruntime.Must(oauth.AddToContainer(s.container, imOperator,
		auth.NewTokenOperator(s.CacheClient, s.Issuer, s.Config.AuthenticationOptions),
		auth.NewPasswordAuthenticator(s.KubernetesClient.KubeSphere(), userLister, s.Config.AuthenticationOptions),
		auth.NewOAuthAuthenticator(s.KubernetesClient.KubeSphere(), userLister, s.Config.AuthenticationOptions),
		auth.NewLoginRecorder(s.KubernetesClient.KubeSphere(), userLister),
		s.Config.AuthenticationOptions))
	urlruntime.Must(version.AddToContainer(s.container, s.KubernetesClient.Kubernetes().Discovery()))
	urlruntime.Must(packagev1alpha1.AddToContainer(s.container, s.RuntimeCache))
}

// installHealthz creates the healthz endpoint for this server
func (s *APIServer) installHealthz() {
	urlruntime.Must(healthz.InstallHandler(s.container, []healthz.HealthChecker{}...))
}

func (s *APIServer) Run(ctx context.Context) (err error) {

	err = s.waitForResourceSync(ctx)
	if err != nil {
		return err
	}

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
			iamv1alpha2.Resource(iamv1alpha2.ResourcesPluralUser),
			iamv1alpha2.Resource(iamv1alpha2.ResourcesPluralGlobalRole),
			iamv1alpha2.Resource(iamv1alpha2.ResourcesPluralGlobalRoleBinding),
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
		handler = filters.WithAuditing(handler,
			audit.NewAuditing(s.InformerFactory, s.Config.AuditingOptions, stopCh))
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
		excludedPaths := []string{"/oauth/*", "/kapis/config.kubesphere.io/*", "/kapis/version", "/kapis/metrics", "/healthz"}
		pathAuthorizer, _ := path.NewAuthorizer(excludedPaths)
		amOperator := am.NewReadOnlyOperator(s.InformerFactory)
		authorizers = unionauthorizer.New(pathAuthorizer, rbac.NewRBACAuthorizer(amOperator))
	}

	handler = filters.WithAuthorization(handler, authorizers)
	handler = filters.WithMulticluster(handler, s.ClusterClient)

	userLister := s.InformerFactory.KubeSphereSharedInformerFactory().Iam().V1alpha2().Users().Lister()
	loginRecorder := auth.NewLoginRecorder(s.KubernetesClient.KubeSphere(), userLister)

	// authenticators are unordered
	authn := unionauth.New(anonymous.NewAuthenticator(),
		basictoken.New(basic.NewBasicAuthenticator(auth.NewPasswordAuthenticator(
			s.KubernetesClient.KubeSphere(),
			userLister,
			s.Config.AuthenticationOptions),
			loginRecorder)),
		bearertoken.New(jwt.NewTokenAuthenticator(
			auth.NewTokenOperator(s.CacheClient, s.Issuer, s.Config.AuthenticationOptions),
			userLister)))
	handler = filters.WithAuthentication(handler, authn)
	handler = filters.WithRequestInfo(handler, requestInfoResolver)
	s.Server.Handler = handler
	return nil
}

func isResourceExists(apiResources []v1.APIResource, resource schema.GroupVersionResource) bool {
	for _, apiResource := range apiResources {
		if apiResource.Name == resource.Resource {
			return true
		}
	}
	return false
}

type informerForResourceFunc func(resource schema.GroupVersionResource) (interface{}, error)

func waitForCacheSync(discoveryClient discovery.DiscoveryInterface, sharedInformerFactory informers.GenericInformerFactory, informerForResourceFunc informerForResourceFunc, GVRs map[schema.GroupVersion][]string, stopCh <-chan struct{}) error {
	for groupVersion, resourceNames := range GVRs {
		var apiResourceList *v1.APIResourceList
		var err error
		err = retry.OnError(retry.DefaultRetry, func(err error) bool {
			return !errors.IsNotFound(err)
		}, func() error {
			apiResourceList, err = discoveryClient.ServerResourcesForGroupVersion(groupVersion.String())
			return err
		})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("group version %s not exists in the cluster", groupVersion)
				continue
			}
			return fmt.Errorf("failed to fetch group version %s: %s", groupVersion, err)
		}
		for _, resourceName := range resourceNames {
			groupVersionResource := groupVersion.WithResource(resourceName)
			if !isResourceExists(apiResourceList.APIResources, groupVersionResource) {
				klog.Warningf("resource %s not exists in the cluster", groupVersionResource)
			} else {
				// reflect.ValueOf(sharedInformerFactory).MethodByName("ForResource").Call([]reflect.Value{reflect.ValueOf(groupVersionResource)})
				if _, err = informerForResourceFunc(groupVersionResource); err != nil {
					return fmt.Errorf("failed to create informer for %s: %s", groupVersionResource, err)
				}
			}
		}
	}
	sharedInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)
	return nil
}

func (s *APIServer) waitForResourceSync(ctx context.Context) error {
	klog.V(0).Info("Start cache objects")

	stopCh := ctx.Done()
	// resources we have to create informer first
	k8sGVRs := map[schema.GroupVersion][]string{
		{Group: "", Version: "v1"}: {
			"namespaces",
			"nodes",
			"resourcequotas",
			"pods",
			"services",
			"persistentvolumeclaims",
			"persistentvolumes",
			"secrets",
			"configmaps",
			"serviceaccounts",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1"}: {
			"roles",
			"rolebindings",
			"clusterroles",
			"clusterrolebindings",
		},
		{Group: "apps", Version: "v1"}: {
			"deployments",
			"daemonsets",
			"replicasets",
			"statefulsets",
			"controllerrevisions",
		},
		{Group: "storage.k8s.io", Version: "v1"}: {
			"storageclasses",
		},
		{Group: "batch", Version: "v1"}: {
			"jobs",
		},
		{Group: "batch", Version: "v1"}: {
			"cronjobs",
		},
		{Group: "networking.k8s.io", Version: "v1"}: {
			"ingresses",
			"networkpolicies",
		},
		{Group: "autoscaling", Version: "v2beta2"}: {
			"horizontalpodautoscalers",
		},
	}

	if err := waitForCacheSync(s.KubernetesClient.Kubernetes().Discovery(),
		s.InformerFactory.KubernetesSharedInformerFactory(),
		func(resource schema.GroupVersionResource) (interface{}, error) {
			return s.InformerFactory.KubernetesSharedInformerFactory().ForResource(resource)
		},
		k8sGVRs, stopCh); err != nil {
		return err
	}

	ksGVRs := map[schema.GroupVersion][]string{
		{Group: "tenant.kubesphere.io", Version: "v1alpha1"}: {
			"workspaces",
		},
		{Group: "tenant.kubesphere.io", Version: "v1alpha2"}: {
			"workspacetemplates",
		},
		{Group: "iam.kubesphere.io", Version: "v1alpha2"}: {
			"users",
			"globalroles",
			"globalrolebindings",
			"groups",
			"groupbindings",
			"workspaceroles",
			"workspacerolebindings",
			"loginrecords",
		},
		{Group: "cluster.kubesphere.io", Version: "v1alpha1"}: {
			"clusters",
		},
	}

	if err := waitForCacheSync(s.KubernetesClient.Kubernetes().Discovery(),
		s.InformerFactory.KubeSphereSharedInformerFactory(),
		func(resource schema.GroupVersionResource) (interface{}, error) {
			return s.InformerFactory.KubeSphereSharedInformerFactory().ForResource(resource)
		},
		ksGVRs, stopCh); err != nil {
		return err
	}

	apiextensionsGVRs := map[schema.GroupVersion][]string{
		{Group: "apiextensions.k8s.io", Version: "v1"}: {
			"customresourcedefinitions",
		},
	}

	if err := waitForCacheSync(s.KubernetesClient.Kubernetes().Discovery(),
		s.InformerFactory.ApiExtensionSharedInformerFactory(), func(resource schema.GroupVersionResource) (interface{}, error) {
			return s.InformerFactory.ApiExtensionSharedInformerFactory().ForResource(resource)
		},
		apiextensionsGVRs, stopCh); err != nil {
		return err
	}

	go s.RuntimeCache.Start(ctx)
	s.RuntimeCache.WaitForCacheSync(ctx)

	klog.V(0).Info("Finished caching objects")
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
	}, resourcev1beta1.New(s.RuntimeClient, s.RuntimeCache))
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
