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

	"kubesphere.io/kubesphere/pkg/controller/options"

	"strings"
	"sync"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"

	"kubesphere.io/kubesphere/pkg/apiserver/auditing"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models/telemetry"
	"kubesphere.io/kubesphere/pkg/models/terminal"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
)

// Package config saves configuration for running KubeSphere components
//
// Config can be configured from command line flags and configuration file.
// Command line flags hold higher priority than configuration file. But if
// component Endpoint/Host/APIServer was left empty, all of that component
// command line flags will be ignored, use configuration file instead.
// For example, we have configuration file
//
// mysql:
//   host: mysql.kubesphere-system.svc
//   username: root
//   password: password
//
// At the same time, have command line flags like following:
//
// --mysql-host mysql.openpitrix-system.svc --mysql-username king --mysql-password 1234
//
// We will use `king:1234@mysql.openpitrix-system.svc` from command line flags rather
// than `root:password@mysql.kubesphere-system.svc` from configuration file,
// cause command line has higher priority. But if command line flags like following:
//
// --mysql-username root --mysql-password password
//
// we will `root:password@mysql.kubesphere-system.svc` as input, cause
// mysql-host is missing in command line flags, all other mysql command line flags
// will be ignored.

var (
	// singleton instance of config package
	_config = defaultConfig()
)

const (
	// DefaultConfigurationName is the default name of configuration
	defaultConfigurationName = "kubesphere"

	// DefaultConfigurationPath the default location of the configuration file
	defaultConfigurationPath = "/etc/kubesphere"

	envPrefix = defaultConfigurationName
)

type config struct {
	cfg      *Config
	loadOnce sync.Once
}

func (c *config) loadFromDisk() (*Config, error) {
	var err error
	c.loadOnce.Do(func() {
		if err = viper.ReadInConfig(); err != nil {
			return
		}
		err = viper.Unmarshal(c.cfg)
	})
	return c.cfg, err
}

func defaultConfig() *config {
	viper.SetConfigName(defaultConfigurationName)
	viper.AddConfigPath(defaultConfigurationPath)

	// Load from current working directory, only used for debugging
	viper.AddConfigPath(".")

	// Load from Environment variables
	viper.SetEnvPrefix(envPrefix)
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	return &config{
		cfg:      New(),
		loadOnce: sync.Once{},
	}
}

// Config defines everything needed for apiserver to deal with external services
type Config struct {
	KubernetesOptions     *k8s.Options                 `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty" mapstructure:"kubernetes"`
	CacheOptions          *cache.Options               `json:"cache,omitempty" yaml:"cache,omitempty" mapstructure:"cache"`
	AuthenticationOptions *authentication.Options      `json:"authentication,omitempty" yaml:"authentication,omitempty" mapstructure:"authentication"`
	AuthorizationOptions  *authorization.Options       `json:"authorization,omitempty" yaml:"authorization,omitempty" mapstructure:"authorization"`
	MultiClusterOptions   *multicluster.Options        `json:"multicluster,omitempty" yaml:"multicluster,omitempty" mapstructure:"multicluster"`
	AuditingOptions       *auditing.Options            `json:"auditing,omitempty" yaml:"auditing,omitempty" mapstructure:"auditing"`
	TerminalOptions       *terminal.Options            `json:"terminal,omitempty" yaml:"terminal,omitempty" mapstructure:"terminal"`
	HelmExecutorOptions   *options.HelmExecutorOptions `json:"helmExecutor,omitempty" yaml:"helmExecutor,omitempty" mapstructure:"helmExecutor"`
	TelemetryOptions      *telemetry.Options           `json:"telemetry,omitempty" yaml:"telemetry,omitempty" mapstructure:"telemetry"`
	ExtensionOptions      *options.ExtensionOptions    `json:"extension,omitempty" yaml:"extension,omitempty" mapstructure:"extension"`
}

// New config creates a default non-empty Config
func New() *Config {
	return &Config{
		KubernetesOptions:     k8s.NewKubernetesOptions(),
		CacheOptions:          cache.NewCacheOptions(),
		AuthenticationOptions: authentication.NewOptions(),
		AuthorizationOptions:  authorization.NewOptions(),
		MultiClusterOptions:   multicluster.NewOptions(),
		TerminalOptions:       terminal.NewOptions(),
		AuditingOptions:       auditing.NewAuditingOptions(),
		TelemetryOptions:      telemetry.NewTelemetryOptions(),
		HelmExecutorOptions:   options.NewHelmExecutorOptions(),
		ExtensionOptions:      options.NewExtensionOptions(),
	}
}

// TryLoadFromDisk loads configuration from default location after server startup
// return nil error if configuration file not exists
func TryLoadFromDisk() (*Config, error) {
	return _config.loadFromDisk()
}

// FromConfigMap returns KubeSphere running config by the given ConfigMap.
func FromConfigMap(cm *corev1.ConfigMap) (*Config, error) {
	c := &Config{}
	value, ok := cm.Data[constants.KubeSphereConfigMapDataKey]
	if !ok {
		return nil, fmt.Errorf("failed to get configmap kubesphere.yaml value")
	}

	if err := yaml.Unmarshal([]byte(value), c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value from configmap. err: %s", err)
	}
	return c, nil
}