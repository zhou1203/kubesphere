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
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbachelper "kubesphere.io/kubesphere/pkg/conponenthelper/auth/rbac"
	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	"kubesphere.io/kubesphere/pkg/controller/cluster/predicate"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const controllerName = "globalrole-controller"

var _ kscontroller.Controller = &Reconciler{}
var _ reconcile.Reconciler = &Reconciler{}

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
	r.logger = mgr.GetLogger().WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	if r.ClusterClientSet == nil {
		return kscontroller.FailedToSetup(controllerName, "ClusterClientSet must not be nil")
	}
	ctr, err := builder.
		ControllerManagedBy(mgr).
		For(&iamv1beta1.GlobalRole{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named(controllerName).
		Build(r)
	if err != nil {
		return kscontroller.FailedToSetup(controllerName, err)
	}
	err = ctr.Watch(
		&source.Kind{Type: &clusterv1alpha1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(r.mapper),
		predicate.ClusterStatusChangedPredicate{})

	if err != nil {
		return kscontroller.FailedToSetup(controllerName, err)
	}
	return nil
}

func (r *Reconciler) mapper(o client.Object) []reconcile.Request {
	cluster := o.(*clusterv1alpha1.Cluster)
	var requests []reconcile.Request
	if !clusterutils.IsClusterReady(cluster) {
		return requests
	}
	globalRoles := &iamv1beta1.GlobalRoleList{}
	if err := r.List(context.Background(), globalRoles); err != nil {
		r.logger.Error(err, "failed to list global roles")
		return requests
	}
	for _, globalRole := range globalRoles.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: globalRole.Name}})
	}
	return requests
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

	if err := r.multiClusterSync(ctx, globalRole); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) multiClusterSync(ctx context.Context, globalRole *iamv1beta1.GlobalRole) error {
	clusters, err := r.ClusterClientSet.ListClusters(ctx)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %s", err)
	}
	var notReadyClusters []string
	for _, cluster := range clusters {
		// skip if cluster is not ready
		if !clusterutils.IsClusterReady(&cluster) {
			notReadyClusters = append(notReadyClusters, cluster.Name)
			continue
		}
		if clusterutils.IsHostCluster(&cluster) {
			continue
		}
		if err := r.syncGlobalRole(ctx, cluster, globalRole); err != nil {
			return fmt.Errorf("failed to sync global role %s to cluster %s: %s", globalRole.Name, cluster.Name, err)
		}
	}
	if len(notReadyClusters) > 0 {
		klog.FromContext(ctx).V(4).Info("cluster not ready", "clusters", strings.Join(notReadyClusters, ","))
		r.recorder.Event(globalRole, corev1.EventTypeWarning, kscontroller.SyncFailed, fmt.Sprintf("cluster not ready: %s", strings.Join(notReadyClusters, ",")))
	}
	return nil
}

func (r *Reconciler) syncGlobalRole(ctx context.Context, cluster clusterv1alpha1.Cluster, globalRole *iamv1beta1.GlobalRole) error {
	if r.ClusterClientSet.IsHostCluster(&cluster) {
		return nil
	}
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %s", err)
	}
	target := &iamv1beta1.GlobalRole{ObjectMeta: metav1.ObjectMeta{Name: globalRole.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, clusterClient, target, func() error {
		target.Labels = globalRole.Labels
		target.Annotations = globalRole.Annotations
		target.Rules = globalRole.Rules
		target.AggregationRoleTemplates = globalRole.AggregationRoleTemplates
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to update global role: %s", err)
	}

	r.logger.V(4).Info("global role successfully synced", "cluster", cluster.Name, "operation", op, "name", globalRole.Name)
	return nil
}
