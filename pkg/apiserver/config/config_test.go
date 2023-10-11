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
	"k8s.io/utils/pointer"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization"
	"kubesphere.io/kubesphere/pkg/models/terminal"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/auditing"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/telemetry"
)

func newTestConfig() (*Config, error) {
	var conf = &Config{
		KubernetesOptions: &k8s.KubernetesOptions{
			KubeConfig: "/Users/zry/.kube/config",
			Master:     "https://127.0.0.1:6443",
			QPS:        1e6,
			Burst:      1e6,
		},
		CacheOptions: &cache.Options{
			Type:    "redis",
			Options: map[string]interface{}{},
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
		AuditingOptions: &auditing.Options{
			Host:        "http://elasticsearch-logging-data.kubesphere-logging-system.svc:9200",
			IndexPrefix: "ks-logstash-auditing",
			Version:     "6",
		},
		TerminalOptions: &terminal.Options{
			NodeShellOptions: terminal.NodeShellOptions{
				Image:   "alpine:3.15",
				Timeout: 600,
			},
			KubectlOptions: terminal.KubectlOptions{
				Image: "kubesphere/kubectl:v1.27.4",
			},
		},
		TelemetryOptions: &telemetry.Options{
			Enabled:             pointer.Bool(true),
			KSCloudURL:          pointer.String("https://clouddev.kubesphere.io"),
			Period:              pointer.Duration(time.Hour * 24),
			ClusterInfoLiveTime: pointer.Duration(time.Hour * 24 * 365),
		},
		HelmImage: "kubesphere/helm:v3.12.1",
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
