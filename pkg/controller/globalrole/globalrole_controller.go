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

package globalrole

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	rbachelper "kubesphere.io/kubesphere/pkg/conponenthelper/auth/rbac"
)

const controllerName = "globalrole-controller"

type Reconciler struct {
	client.Client
	logger   logr.Logger
	recorder record.EventRecorder
	helper   *rbachelper.Helper
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	globalRole := &iamv1beta1.GlobalRole{}
	if err := r.Get(ctx, req.NamespacedName, globalRole); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if globalRole.AggregationRoleTemplates != nil {
		if err := r.helper.AggregationRole(ctx, rbachelper.GlobalRoleRuleOwner{GlobalRole: globalRole}, r.recorder); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.helper = rbachelper.NewHelper(r.Client)
	return builder.
		ControllerManagedBy(mgr).
		For(
			&iamv1alpha2.GlobalRole{},
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
