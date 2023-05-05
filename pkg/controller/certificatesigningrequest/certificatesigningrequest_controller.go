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

package certificatesigningrequest

import (
	"context"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
)

const controllerName = "csr-controller"

type Reconciler struct {
	client.Client
	k8sclient kubernetes.Interface
	recorder  record.EventRecorder

	kubeconfigOperator kubeconfig.Interface
}

func NewReconciler(k8sClient kubernetes.Interface, configMapLister corev1listers.ConfigMapLister, config *rest.Config) *Reconciler {
	return &Reconciler{
		k8sclient:          k8sClient,
		kubeconfigOperator: kubeconfig.NewOperator(k8sClient, configMapLister, config),
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the CertificateSigningRequest with this name
	csr := &certificatesv1.CertificateSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, csr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// csr create by kubesphere auto approve
	if username := csr.Labels[constants.UsernameLabelKey]; username != "" {
		if err := r.Approve(csr); err != nil {
			return ctrl.Result{}, err
		}
		// certificate data is not empty
		if len(csr.Status.Certificate) > 0 {
			if err := r.kubeconfigOperator.UpdateKubeconfig(username, csr); err != nil {
				// kubeconfig not generated
				return ctrl.Result{}, err
			}
			// release
			if err := r.k8sclient.CertificatesV1().CertificateSigningRequests().Delete(context.Background(), csr.Name, *metav1.NewDeleteOptions(0)); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	r.recorder.Event(csr, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor(controllerName)

	return builder.
		ControllerManagedBy(mgr).
		For(
			&certificatesv1.CertificateSigningRequest{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Named(controllerName).
		Complete(r)
}

func (r *Reconciler) Approve(csr *certificatesv1.CertificateSigningRequest) error {
	// is approved
	if len(csr.Status.Certificate) > 0 {
		return nil
	}
	csr.Status = certificatesv1.CertificateSigningRequestStatus{
		Conditions: []certificatesv1.CertificateSigningRequestCondition{{
			Status:  corev1.ConditionTrue,
			Type:    "Approved",
			Reason:  "KubeSphereApprove",
			Message: "This CSR was approved by KubeSphere",
			LastUpdateTime: metav1.Time{
				Time: time.Now(),
			},
		}},
	}

	// approve csr
	if _, err := r.k8sclient.CertificatesV1().CertificateSigningRequests().UpdateApproval(context.Background(), csr.Name, csr, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}
