/*
Copyright 2020 KubeSphere Authors

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

package options

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"

	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	"k8s.io/client-go/kubernetes/scheme"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/apis"
	"kubesphere.io/kubesphere/pkg/apiserver"
	apiserverconfig "kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/informers"
	genericoptions "kubesphere.io/kubesphere/pkg/server/options"
	auditingclient "kubesphere.io/kubesphere/pkg/simple/client/auditing/elasticsearch"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"

	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
)

type ServerRunOptions struct {
	ConfigFile              string
	GenericServerRunOptions *genericoptions.ServerRunOptions
	*apiserverconfig.Config
	schemeOnce sync.Once
	DebugMode  bool

	// Enable gops or not.
	GOPSEnabled bool
}

func NewServerRunOptions() *ServerRunOptions {
	s := &ServerRunOptions{
		GenericServerRunOptions: genericoptions.NewServerRunOptions(),
		Config:                  apiserverconfig.New(),
		schemeOnce:              sync.Once{},
	}

	return s
}

func (s *ServerRunOptions) Flags() (fss cliflag.NamedFlagSets) {
	fs := fss.FlagSet("generic")
	fs.BoolVar(&s.DebugMode, "debug", false, "Don't enable this if you don't know what it means.")
	fs.BoolVar(&s.GOPSEnabled, "gops", false, "Whether to enable gops or not. When enabled this option, "+
		"ks-apiserver will listen on a random port on 127.0.0.1, then you can use the gops tool to list and diagnose the ks-apiserver currently running.")
	s.GenericServerRunOptions.AddFlags(fs, s.GenericServerRunOptions)
	s.KubernetesOptions.AddFlags(fss.FlagSet("kubernetes"), s.KubernetesOptions)
	s.AuthenticationOptions.AddFlags(fss.FlagSet("authentication"), s.AuthenticationOptions)
	s.AuthorizationOptions.AddFlags(fss.FlagSet("authorization"), s.AuthorizationOptions)
	s.MultiClusterOptions.AddFlags(fss.FlagSet("multicluster"), s.MultiClusterOptions)
	s.AuditingOptions.AddFlags(fss.FlagSet("auditing"), s.AuditingOptions)

	fs = fss.FlagSet("klog")
	local := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(local)
	local.VisitAll(func(fl *flag.Flag) {
		fl.Name = strings.Replace(fl.Name, "_", "-", -1)
		fs.AddGoFlag(fl)
	})

	return fss
}

const fakeInterface string = "FAKE"

// NewAPIServer creates an APIServer instance using given options
func (s *ServerRunOptions) NewAPIServer(stopCh <-chan struct{}) (*apiserver.APIServer, error) {
	apiServer := &apiserver.APIServer{
		Config: s.Config,
	}

	kubernetesClient, err := k8s.NewKubernetesClient(s.KubernetesOptions)
	if err != nil {
		return nil, err
	}
	apiServer.KubernetesClient = kubernetesClient

	informerFactory := informers.NewInformerFactories(kubernetesClient.Kubernetes(), kubernetesClient.KubeSphere(), kubernetesClient.ApiExtensions())
	apiServer.InformerFactory = informerFactory

	apiServer.ClusterClient = clusterclient.NewClusterClient(informerFactory.KubeSphereSharedInformerFactory().Cluster().V1alpha1().Clusters())

	// If CacheOptions is nil, will create a fake cache.
	if s.CacheOptions.Type == "" {
		s.CacheOptions.Type = cache.DefaultCacheType
		klog.Warning("ks-apiserver starts without cache provided, it will use in memory cache. " +
			"This may cause inconsistencies when running ks-apiserver with multiple replicas, and memory leak risk")
	}
	cacheClient, err := cache.New(s.CacheOptions, stopCh)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache, error: %v", err)
	}
	apiServer.CacheClient = cacheClient

	if s.AuditingOptions.Host != "" {
		auditingClient, err := auditingclient.NewClient(s.AuditingOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to elasticsearch, please check elasticsearch status, error: %v", err)
		}
		apiServer.AuditingClient = auditingClient
	}

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", s.GenericServerRunOptions.InsecurePort),
	}

	if s.GenericServerRunOptions.SecurePort != 0 {
		certificate, err := tls.LoadX509KeyPair(s.GenericServerRunOptions.TlsCertFile, s.GenericServerRunOptions.TlsPrivateKey)
		if err != nil {
			return nil, err
		}

		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{certificate},
		}
		server.Addr = fmt.Sprintf(":%d", s.GenericServerRunOptions.SecurePort)
	}

	sch := scheme.Scheme
	s.schemeOnce.Do(func() {
		if err := apis.AddToScheme(sch); err != nil {
			klog.Fatalf("unable add APIs to scheme: %v", err)
		}
	})

	mapper, err := apiutil.NewDynamicRESTMapper(apiServer.KubernetesClient.Config())
	if err != nil {
		klog.Fatalf("unable create dynamic RESTMapper: %v", err)
	}

	apiServer.RuntimeCache, err = runtimecache.New(apiServer.KubernetesClient.Config(), runtimecache.Options{Scheme: sch, Mapper: mapper})
	if err != nil {
		klog.Fatalf("unable to create controller runtime cache: %v", err)
	}

	apiServer.RuntimeClient, err = runtimeclient.New(apiServer.KubernetesClient.Config(), runtimeclient.Options{Scheme: sch})
	if err != nil {
		klog.Fatalf("unable to create controller runtime client: %v", err)
	}

	apiServer.Issuer, err = token.NewIssuer(s.AuthenticationOptions)
	if err != nil {
		klog.Fatalf("unable to create issuer: %v", err)
	}

	apiServer.Server = server

	return apiServer, nil
}
