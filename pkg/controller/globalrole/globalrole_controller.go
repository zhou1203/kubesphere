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
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	rbachelper "kubesphere.io/kubesphere/pkg/conponenthelper/auth/rbac"
)

const controllerName = "globalrole-controller"

type Reconciler struct {
	client.Client
	logger           logr.Logger
	recorder         record.EventRecorder
	helper           *rbachelper.Helper
	ClusterClientSet clusterclient.Interface
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.helper = rbachelper.NewHelper(r.Client)
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	ctr, err := builder.
		ControllerManagedBy(mgr).
		For(&iamv1beta1.GlobalRole{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named(controllerName).
		Build(r)

	if err != nil {
		return err
	}

	// host cluster
	if r.ClusterClientSet != nil {
		return ctr.Watch(
			&source.Kind{Type: &clusterv1alpha1.Cluster{}},
			handler.EnqueueRequestsFromMapFunc(r.mapper),
			clusterutils.ClusterStatusChangedPredicate{})
	}

	return nil
}

func (r *Reconciler) mapper(o client.Object) []reconcile.Request {
	cluster := o.(*clusterv1alpha1.Cluster)
	if !clusterutils.IsClusterReady(cluster) {
		return []reconcile.Request{}
	}
	globalRoles := &iamv1beta1.GlobalRoleList{}
	if err := r.List(context.Background(), globalRoles); err != nil {
		r.logger.Error(err, "failed to list global roles")
		return []reconcile.Request{}
	}
	var result []reconcile.Request
	for _, globalRole := range globalRoles.Items {
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: globalRole.Name}})
	}
	return result
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

	if r.ClusterClientSet != nil {
		if err := r.sync(ctx, globalRole); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) sync(ctx context.Context, globalRole *iamv1beta1.GlobalRole) error {
	clusters, err := r.ClusterClientSet.ListCluster(ctx)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		// skip if cluster is not ready
		if !clusterutils.IsClusterReady(&cluster) {
			continue
		}
		if err := r.syncGlobalRole(ctx, cluster, globalRole); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncGlobalRole(ctx context.Context, cluster clusterv1alpha1.Cluster, globalRole *iamv1beta1.GlobalRole) error {
	if r.ClusterClientSet.IsHostCluster(&cluster) {
		return nil
	}
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	target := &iamv1beta1.GlobalRole{}
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: globalRole.Name}, target); err != nil {
		if errors.IsNotFound(err) {
			target = globalRole.DeepCopy()
			return clusterClient.Create(ctx, target)
		}
		return err
	}
	if !reflect.DeepEqual(target.Rules, globalRole.Rules) ||
		!reflect.DeepEqual(target.AggregationRoleTemplates, globalRole.AggregationRoleTemplates) ||
		!reflect.DeepEqual(target.Labels, globalRole.Labels) ||
		!reflect.DeepEqual(target.Annotations, globalRole.Annotations) {
		target.Labels = globalRole.Labels
		target.Annotations = globalRole.Annotations
		target.Rules = globalRole.Rules
		target.AggregationRoleTemplates = globalRole.AggregationRoleTemplates
		return clusterClient.Update(ctx, target)
	}
	return nil
}
