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

package config

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v2"

	networkv1alpha1 "kubesphere.io/api/network/v1alpha1"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization"
	"kubesphere.io/kubesphere/pkg/models/terminal"
	"kubesphere.io/kubesphere/pkg/simple/client/auditing"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/events"
	"kubesphere.io/kubesphere/pkg/simple/client/gateway"
	"kubesphere.io/kubesphere/pkg/simple/client/gpu"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/simple/client/ldap"
	"kubesphere.io/kubesphere/pkg/simple/client/logging"
	"kubesphere.io/kubesphere/pkg/simple/client/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/network"
	"kubesphere.io/kubesphere/pkg/simple/client/s3"
	"kubesphere.io/kubesphere/pkg/simple/client/servicemesh"
)

func newTestConfig() (*Config, error) {
	var conf = &Config{
		KubernetesOptions: &k8s.KubernetesOptions{
			KubeConfig: "/Users/zry/.kube/config",
			Master:     "https://127.0.0.1:6443",
			QPS:        1e6,
			Burst:      1e6,
		},
		ServiceMeshOptions: &servicemesh.Options{
			IstioPilotHost:            "http://istio-pilot.istio-system.svc:9090",
			JaegerQueryHost:           "http://jaeger-query.istio-system.svc:80",
			ServicemeshPrometheusHost: "http://prometheus-k8s.kubesphere-monitoring-system.svc",
		},
		LdapOptions: &ldap.Options{
			Host:            "http://openldap.kubesphere-system.svc",
			ManagerDN:       "cn=admin,dc=example,dc=org",
			ManagerPassword: "P@88w0rd",
			UserSearchBase:  "ou=Users,dc=example,dc=org",
			GroupSearchBase: "ou=Groups,dc=example,dc=org",
			InitialCap:      10,
			MaxCap:          100,
			PoolName:        "ldap",
		},
		CacheOptions: &cache.Options{
			Type:    "redis",
			Options: map[string]interface{}{},
		},
		S3Options: &s3.Options{
			Endpoint:        "http://minio.openpitrix-system.svc",
			Region:          "us-east-1",
			DisableSSL:      false,
			ForcePathStyle:  false,
			AccessKeyID:     "ABCDEFGHIJKLMN",
			SecretAccessKey: "OPQRSTUVWXYZ",
			SessionToken:    "abcdefghijklmn",
			Bucket:          "ssss",
		},
		NetworkOptions: &network.Options{
			EnableNetworkPolicy: true,
			NSNPOptions: network.NSNPOptions{
				AllowedIngressNamespaces: []string{},
			},
			WeaveScopeHost: "weave-scope-app.weave",
			IPPoolType:     networkv1alpha1.IPPoolTypeNone,
		},
		//MonitoringOptions: &prometheus.Options{
		//	Endpoint: "http://prometheus.kubesphere-monitoring-system.svc",
		//},
		LoggingOptions: &logging.Options{
			Host:        "http://elasticsearch-logging.kubesphere-logging-system.svc:9200",
			IndexPrefix: "elk",
			Version:     "6",
		},
		AuthorizationOptions: authorization.NewOptions(),
		AuthenticationOptions: &authentication.Options{
			AuthenticateRateLimiterMaxTries: 5,
			AuthenticateRateLimiterDuration: 30 * time.Minute,
			JwtSecret:                       "xxxxxx",
			LoginHistoryMaximumEntries:      100,
			MultipleLogin:                   false,
			OAuthOptions: &oauth.Options{
				Issuer:            oauth.DefaultIssuer,
				IdentityProviders: []oauth.IdentityProviderOptions{},
				Clients: []oauth.Client{{
					Name:                         "kubesphere-console-client",
					Secret:                       "xxxxxx-xxxxxx-xxxxxx",
					RespondWithChallenges:        true,
					RedirectURIs:                 []string{"http://ks-console.kubesphere-system.svc/oauth/token/implicit"},
					GrantMethod:                  oauth.GrantHandlerAuto,
					AccessTokenInactivityTimeout: nil,
				}},
				AccessTokenMaxAge:            time.Hour * 24,
				AccessTokenInactivityTimeout: 0,
			},
		},
		MultiClusterOptions: multicluster.NewOptions(),
		EventsOptions: &events.Options{
			Host:        "http://elasticsearch-logging-data.kubesphere-logging-system.svc:9200",
			IndexPrefix: "ks-logstash-events",
			Version:     "6",
		},
		AuditingOptions: &auditing.Options{
			Host:        "http://elasticsearch-logging-data.kubesphere-logging-system.svc:9200",
			IndexPrefix: "ks-logstash-auditing",
			Version:     "6",
		},
		GatewayOptions: &gateway.Options{
			WatchesPath: "/etc/kubesphere/watches.yaml",
			Namespace:   "kubesphere-controls-system",
		},
		GPUOptions: &gpu.Options{
			Kinds: []gpu.GPUKind{},
		},
		TerminalOptions: &terminal.Options{
			Image:   "alpine:3.15",
			Timeout: 600,
		},
	}
	return conf, nil
}

func saveTestConfig(t *testing.T, conf *Config) {
	content, err := yaml.Marshal(conf)
	if err != nil {
		t.Fatalf("error marshal config. %v", err)
	}
	err = os.WriteFile(fmt.Sprintf("%s.yaml", defaultConfigurationName), content, 0640)
	if err != nil {
		t.Fatalf("error write configuration file, %v", err)
	}
}

func cleanTestConfig(t *testing.T) {
	file := fmt.Sprintf("%s.yaml", defaultConfigurationName)
	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Log("file not exists, skipping")
		return
	}

	err := os.Remove(file)
	if err != nil {
		t.Fatalf("remove %s file failed", file)
	}

}

func TestGet(t *testing.T) {
	conf, err := newTestConfig()
	if err != nil {
		t.Fatal(err)
	}
	saveTestConfig(t, conf)
	defer cleanTestConfig(t)

	conf2, err := TryLoadFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(conf, conf2); diff != "" {
		t.Fatal(diff)
	}
}

func TestStripEmptyOptions(t *testing.T) {
	var config Config

	config.CacheOptions = &cache.Options{Type: ""}
	config.LdapOptions = &ldap.Options{Host: ""}
	config.NetworkOptions = &network.Options{
		EnableNetworkPolicy: false,
		WeaveScopeHost:      "",
		IPPoolType:          networkv1alpha1.IPPoolTypeNone,
	}
	config.ServiceMeshOptions = &servicemesh.Options{
		IstioPilotHost:            "",
		ServicemeshPrometheusHost: "",
		JaegerQueryHost:           "",
	}
	config.S3Options = &s3.Options{
		Endpoint: "",
	}
	config.LoggingOptions = &logging.Options{Host: ""}
	config.EventsOptions = &events.Options{Host: ""}
	config.AuditingOptions = &auditing.Options{Host: ""}

	config.stripEmptyOptions()

	if config.CacheOptions != nil ||
		config.LdapOptions != nil ||
		config.NetworkOptions != nil ||
		config.ServiceMeshOptions != nil ||
		config.S3Options != nil ||
		config.LoggingOptions != nil ||
		config.EventsOptions != nil ||
		config.AuditingOptions != nil {
		t.Fatal("config stripEmptyOptions failed")
	}
}
