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
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"kubesphere.io/kubesphere/pkg/constants"
	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	"kubesphere.io/kubesphere/pkg/controller/cluster/predicate"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/controller/workspacetemplate/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
)

const (
	controllerName = "workspacerolebinding-controller"
)

// Reconciler reconciles a WorkspaceRoleBinding object
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
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)

	ctr, err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		For(&iamv1beta1.WorkspaceRoleBinding{}).
		Build(r)

	if err != nil {
		return kscontroller.FailedToSetup(controllerName, err)
	}

	err = ctr.Watch(
		&source.Kind{Type: &clusterv1alpha1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(r.mapper),
		predicate.ClusterStatusChangedPredicate{},
	)

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
	workspaceRoleBindings := &iamv1beta1.WorkspaceRoleBindingList{}
	if err := r.List(context.Background(), workspaceRoleBindings); err != nil {
		r.logger.Error(err, "failed to list workspace role bindings")
		return []reconcile.Request{}
	}
	var result []reconcile.Request
	for _, workspaceRoleBinding := range workspaceRoleBindings.Items {
		workspaceTemplate := &tenantv1alpha2.WorkspaceTemplate{}
		workspaceName := workspaceRoleBinding.Labels[tenantv1alpha1.WorkspaceLabel]
		if err := r.Get(context.Background(), types.NamespacedName{Name: workspaceName}, workspaceTemplate); err != nil {
			klog.Errorf("failed to get workspace template %s: %s", workspaceName, err)
			continue
		}
		if utils.WorkspaceTemplateMatchTargetCluster(workspaceTemplate, cluster) {
			result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: workspaceRoleBinding.Name}})
		}
	}
	return result
}

// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=workspacerolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=types.kubefed.io,resources=federatedworkspacerolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces,verbs=get;list;watch;

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	workspaceRoleBinding := &iamv1beta1.WorkspaceRoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, workspaceRoleBinding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.bindWorkspace(ctx, workspaceRoleBinding); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.multiClusterSync(ctx, workspaceRoleBinding); err != nil {
		return ctrl.Result{}, err
	}

	r.recorder.Event(workspaceRoleBinding, corev1.EventTypeNormal, kscontroller.Synced, kscontroller.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) multiClusterSync(ctx context.Context, workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding) error {
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
		if err := r.syncWorkspaceRoleBinding(ctx, cluster, workspaceRoleBinding); err != nil {
			return fmt.Errorf("failed to sync workspace role binding %s to cluster %s: %s", workspaceRoleBinding.Name, cluster.Name, err)
		}
	}
	if len(notReadyClusters) > 0 {
		klog.FromContext(ctx).V(4).Info("cluster not ready", "clusters", strings.Join(notReadyClusters, ","))
		r.recorder.Event(workspaceRoleBinding, corev1.EventTypeWarning, kscontroller.SyncFailed, fmt.Sprintf("cluster not ready: %s", strings.Join(notReadyClusters, ",")))
	}
	return nil
}

func (r *Reconciler) syncWorkspaceRoleBinding(ctx context.Context, cluster clusterv1alpha1.Cluster, workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding) error {
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}

	workspaceTemplate := &tenantv1alpha2.WorkspaceTemplate{}
	if err := r.Get(ctx, types.NamespacedName{Name: workspaceRoleBinding.Labels[tenantv1alpha1.WorkspaceLabel]}, workspaceTemplate); err != nil {
		return client.IgnoreNotFound(err)
	}

	if utils.WorkspaceTemplateMatchTargetCluster(workspaceTemplate, &cluster) {
		target := &iamv1beta1.WorkspaceRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: workspaceRoleBinding.Name}}
		op, err := controllerutil.CreateOrUpdate(ctx, clusterClient, target, func() error {
			target.Labels = workspaceRoleBinding.Labels
			target.Annotations = workspaceRoleBinding.Annotations
			target.RoleRef = workspaceRoleBinding.RoleRef
			target.Subjects = workspaceRoleBinding.Subjects
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to update workspace role binding: %s", err)
		}
		klog.FromContext(ctx).V(4).Info("workspace role binding successfully synced", "cluster", cluster.Name, "operation", op, "name", workspaceRoleBinding.Name)
	} else {
		return client.IgnoreNotFound(clusterClient.DeleteAllOf(ctx, &iamv1beta1.WorkspaceRole{}, client.MatchingLabels{tenantv1alpha1.WorkspaceLabel: workspaceTemplate.Name}))
	}
	return nil
}

func (r *Reconciler) bindWorkspace(ctx context.Context, workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding) error {
	workspaceName := workspaceRoleBinding.Labels[constants.WorkspaceLabelKey]
	if workspaceName == "" {
		return nil
	}
	workspace := &tenantv1alpha2.WorkspaceTemplate{}
	if err := r.Get(ctx, types.NamespacedName{Name: workspaceName}, workspace); err != nil {
		// skip if workspace not found
		return client.IgnoreNotFound(err)
	}
	// owner reference not match workspace label
	if !metav1.IsControlledBy(workspaceRoleBinding, workspace) {
		workspaceRoleBinding.OwnerReferences = k8sutil.RemoveWorkspaceOwnerReference(workspaceRoleBinding.OwnerReferences)
		if err := controllerutil.SetControllerReference(workspace, workspaceRoleBinding, r.Scheme()); err != nil {
			return err
		}
		if err := r.Update(ctx, workspaceRoleBinding); err != nil {
			return err
		}
	}
	return nil
}
