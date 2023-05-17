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

	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"kubesphere.io/kubesphere/pkg/apiserver"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	apiserverconfig "kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/informers"
	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
	"kubesphere.io/kubesphere/pkg/scheme"
	genericoptions "kubesphere.io/kubesphere/pkg/server/options"
	auditingclient "kubesphere.io/kubesphere/pkg/simple/client/auditing/elasticsearch"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

type ServerRunOptions struct {
	*apiserverconfig.Config
	GenericServerRunOptions *genericoptions.ServerRunOptions

	ConfigFile string
	DebugMode  bool
}

func NewServerRunOptions() *ServerRunOptions {
	return &ServerRunOptions{
		GenericServerRunOptions: genericoptions.NewServerRunOptions(),
		Config:                  apiserverconfig.New(),
	}
}

func (s *ServerRunOptions) Flags() (fss cliflag.NamedFlagSets) {
	fs := fss.FlagSet("generic")
	fs.BoolVar(&s.DebugMode, "debug", false, "Don't enable this if you don't know what it means.")
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

	if apiServer.CacheClient, err = cache.New(s.CacheOptions, stopCh); err != nil {
		return nil, fmt.Errorf("failed to create cache, error: %v", err)
	}

	if s.AuditingOptions.Host != "" {
		if apiServer.AuditingClient, err = auditingclient.NewClient(s.AuditingOptions); err != nil {
			return nil, fmt.Errorf("failed to connect to elasticsearch, please check elasticsearch status, error: %v", err)
		}
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

	mapper, err := apiutil.NewDynamicRESTMapper(apiServer.KubernetesClient.Config())
	if err != nil {
		klog.Fatalf("unable create dynamic RESTMapper: %v", err)
	}

	apiServer.RuntimeCache, err = runtimecache.New(apiServer.KubernetesClient.Config(), runtimecache.Options{Scheme: scheme.Scheme, Mapper: mapper})
	if err != nil {
		klog.Fatalf("unable to create controller runtime cache: %v", err)
	}

	apiServer.RuntimeClient, err = runtimeclient.New(apiServer.KubernetesClient.Config(), runtimeclient.Options{Scheme: scheme.Scheme})
	if err != nil {
		klog.Fatalf("unable to create controller runtime client: %v", err)
	}

	apiServer.Issuer, err = token.NewIssuer(s.AuthenticationOptions)
	if err != nil {
		klog.Fatalf("unable to create issuer: %v", err)
	}

	apiServer.ResourceManager = resourcev1beta1.New(apiServer.RuntimeClient, apiServer.RuntimeCache)

	apiServer.Server = server

	return apiServer, nil
}
