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

package workspacerolebinding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kubesphere.io/kubesphere/pkg/controller"
	kscontroller "kubesphere.io/kubesphere/pkg/controller/options"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/multicluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var k8sManager ctrl.Manager
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

func TestWorkspaceRoleBindingController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WorkspaceRoleBinding Controller Test Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(klog.NewKlogr())

	By("bootstrapping test environment")
	t := true
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		testEnv = &envtest.Environment{
			UseExistingCluster: &t,
		}
	} else {
		testEnv = &envtest.Environment{
			CRDDirectoryPaths:        []string{filepath.Join("..", "..", "..", "config", "ks-core", "crds")},
			AttachControlPlaneOutput: false,
		}
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	clusterClient, err := clusterclient.NewClusterClientSet(k8sManager.GetCache())
	Expect(err).ToNot(HaveOccurred())

	err = (&Reconciler{}).SetupWithManager(&controller.Manager{ClusterClient: clusterClient, Manager: k8sManager,
		Options: kscontroller.Options{MultiClusterOptions: &multicluster.Options{ClusterRole: string(clusterv1alpha1.ClusterRoleHost)}}})
	Expect(err).ToNot(HaveOccurred())

	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	Eventually(func() error {
		return testEnv.Stop()
	}, 30*time.Second, 5*time.Second).ShouldNot(HaveOccurred())
})
