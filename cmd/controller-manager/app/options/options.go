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
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/leaderelection"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"

	controllerconfig "kubesphere.io/kubesphere/pkg/apiserver/config"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/telemetry"
)

type KubeSphereControllerManagerOptions struct {
	KubernetesOptions     *k8s.KubernetesOptions
	AuthenticationOptions *authentication.Options
	MultiClusterOptions   *multicluster.Options
	TelemetryOptions      *telemetry.Options
	// HelmImage defines the Pod image used by the helm executor.
	HelmImage      string `json:"helmImage,omitempty" yaml:"helmImage,omitempty"`
	LeaderElect    bool
	LeaderElection *leaderelection.LeaderElectionConfig
	WebhookCertDir string

	// KubeSphere is using sigs.k8s.io/application as fundamental object to implement Application Management.
	// There are other projects also built on sigs.k8s.io/application, when KubeSphere installed alongside
	// them, conflicts happen. So we leave an option to only reconcile applications  matched with the given
	// selector. Default will reconcile all applications.
	//    For example
	//      "kubesphere.io/creator=" means reconcile applications with this label key
	//      "!kubesphere.io/creator" means exclude applications with this key
	ApplicationSelector string

	// ControllerGates is the list of controller gates to enable or disable controller.
	// '*' means "all enabled by default controllers"
	// 'foo' means "enable 'foo'"
	// '-foo' means "disable 'foo'"
	// first item for a particular name wins.
	//     e.g. '-foo,foo' means "disable foo", 'foo,-foo' means "enable foo"
	// * has the lowest priority.
	//     e.g. *,-foo, means "disable 'foo'"
	ControllerGates []string

	DebugMode bool
}

func NewKubeSphereControllerManagerOptions() *KubeSphereControllerManagerOptions {
	return &KubeSphereControllerManagerOptions{
		KubernetesOptions:     k8s.NewKubernetesOptions(),
		MultiClusterOptions:   multicluster.NewOptions(),
		AuthenticationOptions: authentication.NewOptions(),
		HelmImage:             "kubesphere/helm:v3.12.1",
		LeaderElection: &leaderelection.LeaderElectionConfig{
			LeaseDuration: 30 * time.Second,
			RenewDeadline: 15 * time.Second,
			RetryPeriod:   5 * time.Second,
		},
		LeaderElect:         false,
		WebhookCertDir:      "",
		ApplicationSelector: "",
		ControllerGates:     []string{"*"},
	}
}

func (s *KubeSphereControllerManagerOptions) Flags(allControllerNameSelectors []string) cliflag.NamedFlagSets {
	fss := cliflag.NamedFlagSets{}

	s.KubernetesOptions.AddFlags(fss.FlagSet("kubernetes"), s.KubernetesOptions)
	s.AuthenticationOptions.AddFlags(fss.FlagSet("authentication"), s.AuthenticationOptions)
	s.MultiClusterOptions.AddFlags(fss.FlagSet("multicluster"), s.MultiClusterOptions)
	fs := fss.FlagSet("leaderelection")
	s.bindLeaderElectionFlags(s.LeaderElection, fs)

	fs.BoolVar(&s.LeaderElect, "leader-elect", s.LeaderElect, ""+
		"Whether to enable leader election. This field should be enabled when controller manager"+
		"deployed with multiple replicas.")

	fs.StringVar(&s.WebhookCertDir, "webhook-cert-dir", s.WebhookCertDir, ""+
		"Certificate directory used to setup webhooks, need tls.crt and tls.key placed inside."+
		"if not set, webhook server would look up the server key and certificate in"+
		"{TempDir}/k8s-webhook-server/serving-certs")

	gfs := fss.FlagSet("generic")
	gfs.StringVar(&s.ApplicationSelector, "application-selector", s.ApplicationSelector, ""+
		"Only reconcile application(sigs.k8s.io/application) objects match given selector, this could avoid conflicts with "+
		"other projects built on top of sig-application. Default behavior is to reconcile all of application objects.")
	gfs.StringSliceVar(&s.ControllerGates, "controllers", []string{"*"}, fmt.Sprintf(""+
		"A list of controllers to enable. '*' enables all on-by-default controllers, 'foo' enables the controller "+
		"named 'foo', '-foo' disables the controller named 'foo'.\nAll controllers: %s",
		strings.Join(allControllerNameSelectors, ", ")))

	gfs.StringVar(&s.HelmImage, "helm-image", s.HelmImage, "The Pod image used by the helm executor.")

	gfs.BoolVar(&s.DebugMode, "debug", false, "Don't enable this if you don't know what it means.")

	kfs := fss.FlagSet("klog")
	local := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(local)
	local.VisitAll(func(fl *flag.Flag) {
		fl.Name = strings.Replace(fl.Name, "_", "-", -1)
		kfs.AddGoFlag(fl)
	})

	return fss
}

// Validate Options and Genetic Options
func (s *KubeSphereControllerManagerOptions) Validate(allControllerNameSelectors []string) []error {
	var errs []error
	errs = append(errs, s.KubernetesOptions.Validate()...)
	errs = append(errs, s.MultiClusterOptions.Validate()...)

	// genetic option: application-selector
	if len(s.ApplicationSelector) != 0 {
		_, err := labels.Parse(s.ApplicationSelector)
		if err != nil {
			errs = append(errs, err)
		}
	}

	// genetic option: controllers, check all selectors are valid
	allControllersNameSet := sets.New(allControllerNameSelectors...)
	for _, selector := range s.ControllerGates {
		if selector == "*" {
			continue
		}
		selector = strings.TrimPrefix(selector, "-")
		if !allControllersNameSet.Has(selector) {
			errs = append(errs, fmt.Errorf("%q is not in the list of known controllers", selector))
		}
	}

	return errs
}

// IsControllerEnabled check if a specified controller enabled or not.
func (s *KubeSphereControllerManagerOptions) IsControllerEnabled(name string) bool {
	hasStar := false
	for _, ctrl := range s.ControllerGates {
		if ctrl == name {
			return true
		}
		if ctrl == "-"+name {
			return false
		}
		if ctrl == "*" {
			hasStar = true
		}
	}

	return hasStar
}

func (s *KubeSphereControllerManagerOptions) bindLeaderElectionFlags(l *leaderelection.LeaderElectionConfig, fs *pflag.FlagSet) {
	fs.DurationVar(&l.LeaseDuration, "leader-elect-lease-duration", l.LeaseDuration, ""+
		"The duration that non-leader candidates will wait after observing a leadership "+
		"renewal until attempting to acquire leadership of a led but unrenewed leader "+
		"slot. This is effectively the maximum duration that a leader can be stopped "+
		"before it is replaced by another candidate. This is only applicable if leader "+
		"election is enabled.")
	fs.DurationVar(&l.RenewDeadline, "leader-elect-renew-deadline", l.RenewDeadline, ""+
		"The interval between attempts by the acting master to renew a leadership slot "+
		"before it stops leading. This must be less than or equal to the lease duration. "+
		"This is only applicable if leader election is enabled.")
	fs.DurationVar(&l.RetryPeriod, "leader-elect-retry-period", l.RetryPeriod, ""+
		"The duration the clients should wait between attempting acquisition and renewal "+
		"of a leadership. This is only applicable if leader election is enabled.")
}

// MergeConfig merge new config without validation
// When misconfigured, the app should just crash directly
func (s *KubeSphereControllerManagerOptions) MergeConfig(cfg *controllerconfig.Config) {
	s.KubernetesOptions = cfg.KubernetesOptions
	s.AuthenticationOptions = cfg.AuthenticationOptions
	s.MultiClusterOptions = cfg.MultiClusterOptions
}
