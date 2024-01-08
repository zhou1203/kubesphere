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

package kubeconfig

import (
	"context"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
)

const (
	controllerName = "kubeconfig"
)

var _ kscontroller.Controller = &Reconciler{}
var _ reconcile.Reconciler = &Reconciler{}

// Reconciler reconciles a User object
type Reconciler struct {
	client.Client
	KubeConfigOperator kubeconfig.Interface
}

func (r *Reconciler) Name() string {
	return controllerName
}

func (r *Reconciler) SetupWithManager(mgr *kscontroller.Manager) error {
	r.KubeConfigOperator = kubeconfig.NewOperator(mgr.GetClient(), mgr.K8sClient.Config())
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&iamv1beta1.User{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	user := &iamv1beta1.User{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !user.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if err := r.KubeConfigOperator.CreateKubeConfig(user); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
