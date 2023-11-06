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

package app

import (
	"context"
	"fmt"

	"github.com/google/gops/agent"
	"github.com/spf13/cobra"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	"kubesphere.io/kubesphere/cmd/controller-manager/app/options"
	controllerconfig "kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/controller/application"
	"kubesphere.io/kubesphere/pkg/controller/cluster"
	"kubesphere.io/kubesphere/pkg/controller/ksserviceaccount"
	"kubesphere.io/kubesphere/pkg/controller/quota"
	"kubesphere.io/kubesphere/pkg/controller/user"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/utils/term"
	"kubesphere.io/kubesphere/pkg/version"
)

func NewControllerManagerCommand() *cobra.Command {
	s := options.NewKubeSphereControllerManagerOptions()
	conf, err := controllerconfig.TryLoadFromDisk()
	if err == nil {
		// make sure LeaderElection is not nil
		s = &options.KubeSphereControllerManagerOptions{
			KubernetesOptions:     conf.KubernetesOptions,
			AuthenticationOptions: conf.AuthenticationOptions,
			MultiClusterOptions:   conf.MultiClusterOptions,
			TelemetryOptions:      conf.TelemetryOptions,
			HelmImage:             conf.HelmImage,
			LeaderElection:        s.LeaderElection,
			LeaderElect:           s.LeaderElect,
			WebhookCertDir:        s.WebhookCertDir,
		}
	} else {
		klog.Fatalf("Failed to load configuration from disk: %v", err)
	}

	cmd := &cobra.Command{
		Use:  "controller-manager",
		Long: `KubeSphere controller manager is a daemon that embeds the control loops shipped with KubeSphere.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if errs := s.Validate(allControllers); len(errs) != 0 {
				return utilerrors.NewAggregate(errs)
			}

			if s.DebugMode {
				// Add agent to report additional information such as the current stack trace, Go version, memory stats, etc.
				// Bind to a random port on address 127.0.0.1
				if err := agent.Listen(agent.Options{}); err != nil {
					klog.Fatalln(err)
				}
			}

			return Run(s, controllerconfig.WatchConfigChange(), signals.SetupSignalHandler())
		},
		SilenceUsage: true,
	}

	fs := cmd.Flags()
	namedFlagSets := s.Flags(allControllers)

	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	usageFmt := "Usage:\n  %s\n"
	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStdout(), namedFlagSets, cols)
	})

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of KubeSphere controller-manager",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println(version.Get())
		},
	}

	cmd.AddCommand(versionCmd)

	return cmd
}

func Run(s *options.KubeSphereControllerManagerOptions, configCh <-chan controllerconfig.Config, ctx context.Context) error {
	ictx, cancelFunc := context.WithCancel(context.TODO())
	errCh := make(chan error)
	defer close(errCh)
	go func() {
		if err := run(s, ictx); err != nil {
			errCh <- err
		}
	}()

	// The ctx (signals.SetupSignalHandler()) is to control the entire program life cycle,
	// The ictx(internal context)  is created here to control the life cycle of the controller-manager(all controllers, sharedInformer, webhook etc.)
	// when config changed, stop server and renew context, start new server
	for {
		select {
		case <-ctx.Done():
			cancelFunc()
			return nil
		case cfg := <-configCh:
			cancelFunc()
			s.MergeConfig(&cfg)
			ictx, cancelFunc = context.WithCancel(context.TODO())
			go func() {
				if err := run(s, ictx); err != nil {
					errCh <- err
				}
			}()
		case err := <-errCh:
			cancelFunc()
			return err
		}
	}
}

func run(s *options.KubeSphereControllerManagerOptions, ctx context.Context) error {
	kubernetesClient, err := k8s.NewKubernetesClient(s.KubernetesOptions)
	if err != nil {
		klog.Errorf("Failed to create kubernetes clientset %v", err)
		return err
	}

	webhookServer := webhook.NewServer(webhook.Options{
		CertDir: s.WebhookCertDir,
		Port:    8443,
	})
	mgrOptions := manager.Options{
		Scheme:        scheme.Scheme,
		WebhookServer: webhookServer,
	}

	if s.LeaderElect {
		mgrOptions = manager.Options{
			Scheme:                  scheme.Scheme,
			WebhookServer:           webhookServer,
			LeaderElection:          s.LeaderElect,
			LeaderElectionNamespace: "kubesphere-system",
			LeaderElectionID:        "ks-controller-manager-leader-election",
			LeaseDuration:           &s.LeaderElection.LeaseDuration,
			RetryPeriod:             &s.LeaderElection.RetryPeriod,
			RenewDeadline:           &s.LeaderElection.RenewDeadline,
		}
	}

	klog.V(0).Info("setting up manager")
	ctrl.SetLogger(klog.NewKlogr())
	// Use 8443 instead of 443 cause we need root permission to bind port 443
	mgr, err := manager.New(kubernetesClient.Config(), mgrOptions)
	if err != nil {
		klog.Fatalf("unable to set up overall controller manager: %v", err)
	}

	// TODO(jeff): refactor config with CRD
	// install all controllers
	if err = addAllControllers(mgr, kubernetesClient, s); err != nil {
		klog.Fatalf("unable to register controllers to the manager: %v", err)
	}

	// Setup webhooks
	klog.V(2).Info("registering webhooks to the webhook server")
	if s.IsControllerEnabled("cluster") {
		if err = cluster.SetupWebhookWithManager(mgr); err != nil {
			klog.Fatalf("unable to register cluster webhook: %v", err)
		}
	}
	if err = user.SetupWebhookWithManager(mgr); err != nil {
		klog.Fatalf("unable to register user webhook: %v", err)
	}
	if err = application.SetupWebhookWithManager(mgr); err != nil {
		klog.Fatalf("unable to register application webhook: %v", err)
	}

	resourceQuotaAdmission, err := quota.NewResourceQuotaAdmission(mgr.GetClient(), mgr.GetScheme())
	if err != nil {
		klog.Fatalf("unable to create resource quota admission: %v", err)
	}
	mgr.GetWebhookServer().Register("/validate-quota-kubesphere-io-v1alpha2", &webhook.Admission{Handler: resourceQuotaAdmission})
	mgr.GetWebhookServer().Register("/convert", conversion.NewWebhookHandler(scheme.Scheme))

	decoder := admission.NewDecoder(mgr.GetScheme())
	serviceAccountPodInjector := &ksserviceaccount.PodInjector{
		Log:     mgr.GetLogger(),
		Decoder: decoder,
		Client:  mgr.GetClient(),
	}
	mgr.GetWebhookServer().Register("/serviceaccount-pod-injector", &webhook.Admission{Handler: serviceAccountPodInjector})

	klog.V(0).Info("Starting the controllers.")
	if err = mgr.Start(ctx); err != nil {
		klog.Fatalf("unable to run the manager: %v", err)
	}

	return nil
}
