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
	"reflect"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
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
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.logger.GetSink() == nil {
		r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	}

	if r.recorder == nil {
		r.recorder = mgr.GetEventRecorderFor(controllerName)
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		For(&iamv1beta1.WorkspaceRoleBinding{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=workspacerolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=types.kubefed.io,resources=federatedworkspacerolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces,verbs=get;list;watch;

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("workspacerolebinding", req.NamespacedName)
	rootCtx := context.Background()
	workspaceRoleBinding := &iamv1beta1.WorkspaceRoleBinding{}
	if err := r.Get(rootCtx, req.NamespacedName, workspaceRoleBinding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.bindWorkspace(rootCtx, logger, workspaceRoleBinding); err != nil {
		return ctrl.Result{}, err
	}

	if r.ClusterClientSet != nil {
		if err := r.sync(ctx, workspaceRoleBinding); err != nil {
			return ctrl.Result{}, err
		}
	}

	r.recorder.Event(workspaceRoleBinding, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) sync(ctx context.Context, workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding) error {
	clusters, err := r.ClusterClientSet.ListCluster(ctx)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if err := r.syncWorkspaceRoleBinding(ctx, cluster, workspaceRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncWorkspaceRoleBinding(ctx context.Context, cluster clusterv1alpha1.Cluster, template *iamv1beta1.WorkspaceRoleBinding) error {
	if r.ClusterClientSet.IsHostCluster(&cluster) {
		return nil
	}
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	workspaceRoleBinding := &iamv1beta1.WorkspaceRoleBinding{}
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: template.Name}, workspaceRoleBinding); err != nil {
		if errors.IsNotFound(err) {
			workspaceRoleBinding = &iamv1beta1.WorkspaceRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:        template.Name,
					Labels:      template.Labels,
					Annotations: template.Annotations,
				},
				Subjects: template.Subjects,
				RoleRef:  template.RoleRef,
			}
			return clusterClient.Create(ctx, workspaceRoleBinding)
		}
		return err
	}
	if !reflect.DeepEqual(workspaceRoleBinding.RoleRef, template.RoleRef) ||
		!reflect.DeepEqual(workspaceRoleBinding.Subjects, template.Subjects) ||
		!reflect.DeepEqual(workspaceRoleBinding.Labels, template.Labels) ||
		!reflect.DeepEqual(workspaceRoleBinding.Annotations, template.Annotations) {
		workspaceRoleBinding.Labels = template.Labels
		workspaceRoleBinding.Annotations = template.Annotations
		workspaceRoleBinding.RoleRef = template.RoleRef
		workspaceRoleBinding.Subjects = template.Subjects
		return clusterClient.Update(ctx, workspaceRoleBinding)
	}
	return err
}

func (r *Reconciler) bindWorkspace(ctx context.Context, logger logr.Logger, workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding) error {
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
			logger.Error(err, "set controller reference failed")
			return err
		}
		logger.V(4).Info("update owner reference")
		if err := r.Update(ctx, workspaceRoleBinding); err != nil {
			logger.Error(err, "update owner reference failed")
			return err
		}
	}
	return nil
}
