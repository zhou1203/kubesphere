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

package globalrolebinding

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	"kubesphere.io/kubesphere/pkg/controller/cluster/predicate"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const controllerName = "globalrolebinding-controller"

var _ kscontroller.Controller = &Reconciler{}
var _ reconcile.Reconciler = &Reconciler{}

type Reconciler struct {
	client.Client
	logger           logr.Logger
	recorder         record.EventRecorder
	ClusterClientSet clusterclient.Interface
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.ClusterClientSet == nil {
		return kscontroller.FailedToSetup(controllerName, "ClusterClientSet must not be nil")
	}
	r.Client = mgr.GetClient()
	r.logger = mgr.GetLogger().WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	ctr, err := builder.
		ControllerManagedBy(mgr).
		For(&iamv1beta1.GlobalRoleBinding{}).
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
	if !clusterutils.IsClusterReady(cluster) {
		return []reconcile.Request{}
	}
	globalRoleBindings := &iamv1beta1.GlobalRoleBindingList{}
	if err := r.List(context.Background(), globalRoleBindings); err != nil {
		r.logger.Error(err, "failed to list global role bindings")
		return []reconcile.Request{}
	}
	var result []reconcile.Request
	for _, globalRoleBinding := range globalRoleBindings.Items {
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: globalRoleBinding.Name}})
	}
	return result
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	globalRoleBinding := &iamv1beta1.GlobalRoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, globalRoleBinding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.multiClusterSync(ctx, globalRoleBinding); err != nil {
		return ctrl.Result{}, err
	}

	r.recorder.Event(globalRoleBinding, corev1.EventTypeNormal, kscontroller.Synced, kscontroller.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) assignClusterAdminRole(ctx context.Context, clusterName string, clusterClient client.Client, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	username := globalRoleBinding.Labels[iamv1beta1.UserReferenceLabel]
	if username == "" {
		return nil
	}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s", username, iamv1beta1.ClusterAdmin)}}
	op, err := controllerutil.CreateOrUpdate(ctx, clusterClient, clusterRoleBinding, func() error {
		clusterRoleBinding.Labels = map[string]string{iamv1beta1.RoleReferenceLabel: iamv1beta1.ClusterAdmin, iamv1beta1.UserReferenceLabel: username}
		clusterRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:     rbacv1.UserKind,
				APIGroup: rbacv1.GroupName,
				Name:     username,
			},
		}
		clusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     iamv1beta1.ResourceKindClusterRole,
			Name:     iamv1beta1.ClusterAdmin,
		}
		if err := controllerutil.SetControllerReference(globalRoleBinding, clusterRoleBinding, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference: %s", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to update cluster admin role binding %s: %s", clusterRoleBinding.Name, err)
	}
	r.logger.V(4).Info("cluster admin role binding successfully synced", "cluster", clusterName, "operation", op, "name", globalRoleBinding.Name)
	return nil
}

func (r *Reconciler) multiClusterSync(ctx context.Context, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
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
		if err := r.syncGlobalRoleBinding(ctx, &cluster, globalRoleBinding); err != nil {
			return fmt.Errorf("failed to sync global role binding %s to cluster %s: %s", globalRoleBinding.Name, cluster.Name, err)
		}
	}
	if len(notReadyClusters) > 0 {
		klog.FromContext(ctx).V(4).Info("cluster not ready", "clusters", strings.Join(notReadyClusters, ","))
		r.recorder.Event(globalRoleBinding, corev1.EventTypeWarning, kscontroller.SyncFailed, fmt.Sprintf("cluster not ready: %s", strings.Join(notReadyClusters, ",")))
	}
	return nil
}

func (r *Reconciler) syncGlobalRoleBinding(ctx context.Context, cluster *clusterv1alpha1.Cluster, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %s", err)
	}
	target := &iamv1beta1.GlobalRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: globalRoleBinding.Name}}
	op, err := controllerutil.CreateOrUpdate(ctx, clusterClient, target, func() error {
		target.Labels = globalRoleBinding.Labels
		target.Annotations = globalRoleBinding.Annotations
		target.RoleRef = globalRoleBinding.RoleRef
		target.Subjects = globalRoleBinding.Subjects
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to update global role binding: %s", err)
	}
	if globalRoleBinding.RoleRef.Name == iamv1beta1.PlatformAdmin {
		if err := r.assignClusterAdminRole(ctx, cluster.Name, clusterClient, target); err != nil {
			return fmt.Errorf("failed to assign cluster admin: %s", err)
		}
	}
	r.logger.V(4).Info("global role binding successfully synced", "cluster", cluster.Name, "operation", op, "name", globalRoleBinding.Name)
	return nil
}
