package options

import (
	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/models/telemetry"
	"kubesphere.io/kubesphere/pkg/models/terminal"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
)

type Options struct {
	KubernetesOptions     *k8s.Options
	AuthenticationOptions *authentication.Options
	MultiClusterOptions   *multicluster.Options
	TelemetryOptions      *telemetry.Options
	TerminalOptions       *terminal.Options
	HelmExecutorOptions   *HelmExecutorOptions
	ExtensionOptions      *ExtensionOptions
}

type HelmExecutorOptions struct {
	Image string `json:"image,omitempty" yaml:"image,omitempty" mapstructure:"image,omitempty"`
}

func NewHelmExecutorOptions() *HelmExecutorOptions {
	return &HelmExecutorOptions{Image: "kubesphere/helm:v3.12.1"}
}

type ExtensionOptions struct {
	ImageRegistry string            `json:"imageRegistry,omitempty" yaml:"imageRegistry,omitempty" mapstructure:"imageRegistry,omitempty"`
	NodeSelector  map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty" mapstructure:"nodeSelector,omitempty"`
}

func NewExtensionOptions() *ExtensionOptions {
	return &ExtensionOptions{}
}
