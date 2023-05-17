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

package loginrecord

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"kubesphere.io/kubesphere/pkg/scheme"
)

func TestLoginRecordController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LoginRecord Controller Test Suite")
}

func newLoginRecord(username string) *iamv1alpha2.LoginRecord {
	return &iamv1alpha2.LoginRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%d", username, rand.Intn(1000000)),
			Labels: map[string]string{
				iamv1alpha2.UserReferenceLabel: username,
			},
			CreationTimestamp: metav1.Now(),
		},
		Spec: iamv1alpha2.LoginRecordSpec{
			Type:      iamv1alpha2.Token,
			Provider:  "",
			Success:   true,
			Reason:    iamv1alpha2.AuthenticatedSuccessfully,
			SourceIP:  "",
			UserAgent: "",
		},
	}
}

func newUser(username string) *iamv1alpha2.User {
	return &iamv1alpha2.User{
		ObjectMeta: metav1.ObjectMeta{Name: username},
	}
}

var _ = Describe("LoginRecord", func() {
	var user *iamv1alpha2.User
	var loginRecord *iamv1alpha2.LoginRecord
	var reconciler *Reconciler
	BeforeEach(func() {
		user = newUser("admin")
		loginRecord = newLoginRecord(user.Name)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(user, loginRecord).Build()

		reconciler = NewReconciler(time.Hour, 1)
		reconciler.InjectClient(fakeClient)
		reconciler.recorder = record.NewFakeRecorder(2)
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("LoginRecord Controller", func() {
		It("Should create successfully", func() {

			By("Expecting to reconcile successfully")
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{
				Name: loginRecord.Name,
			}})
			Expect(err).Should(BeNil())
		})
	})
})
