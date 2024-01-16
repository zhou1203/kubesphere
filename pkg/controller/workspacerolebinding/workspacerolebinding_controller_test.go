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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1beta1 "kubesphere.io/api/tenant/v1beta1"
)

var _ = Describe("WorkspaceRoleBinding", func() {

	const timeout = time.Second * 30
	const interval = time.Second * 1

	workspace := &tenantv1beta1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workspace1",
		},
	}
	BeforeEach(func() {
		// Create workspace
		Expect(k8sClient.Create(context.Background(), workspace)).Should(Succeed())
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("WorkspaceRoleBinding Controller", func() {
		It("Should create successfully", func() {
			workspaceAdminBinding := &iamv1beta1.WorkspaceRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "admin-workspace1-admin",
					Labels: map[string]string{tenantv1beta1.WorkspaceLabel: workspace.Name},
				},
			}

			// Create workspace role binding
			Expect(k8sClient.Create(context.Background(), workspaceAdminBinding)).Should(Succeed())

			By("Expecting to create workspace role successfully")
			Eventually(func() bool {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: workspaceAdminBinding.Name}, workspaceAdminBinding)
				return !workspaceAdminBinding.CreationTimestamp.IsZero()
			}, timeout, interval).Should(BeTrue())

			By("Expecting to set owner reference successfully")
			Eventually(func() bool {
				k8sClient.Get(context.Background(), types.NamespacedName{Name: workspaceAdminBinding.Name}, workspaceAdminBinding)
				return len(workspaceAdminBinding.OwnerReferences) > 0
			}, timeout, interval).Should(BeTrue())

			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: workspace.Name}, workspace)).Should(Succeed())

			controlled := true
			expectedOwnerReference := metav1.OwnerReference{
				Kind:               workspace.Kind,
				APIVersion:         workspace.APIVersion,
				UID:                workspace.UID,
				Name:               workspace.Name,
				Controller:         &controlled,
				BlockOwnerDeletion: &controlled,
			}

			By("Expecting to bind workspace successfully")
			Expect(workspaceAdminBinding.OwnerReferences).To(ContainElement(expectedOwnerReference))
		})
	})
})
