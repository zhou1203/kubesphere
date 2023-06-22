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

package workspacetemplate

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
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
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/controller/workspacetemplate/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const (
	controllerName             = "workspacetemplate-controller"
	workspaceTemplateFinalizer = "finalizers.workspacetemplate.kubesphere.io"
	orphanFinalizer            = "orphan.finalizers.kubesphere.io"
)

// Reconciler reconciles a WorkspaceRoleBinding object
type Reconciler struct {
	client.Client
	logger           logr.Logger
	recorder         record.EventRecorder
	ClusterClientSet clusterclient.Interface
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)

	ctr, err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		For(&tenantv1alpha2.WorkspaceTemplate{}).
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
	workspaceTemplates := &tenantv1alpha2.WorkspaceTemplateList{}
	if err := r.List(context.Background(), workspaceTemplates); err != nil {
		r.logger.Error(err, "failed to list workspace templates")
		return []reconcile.Request{}
	}
	var result []reconcile.Request
	for _, workspaceTemplate := range workspaceTemplates.Items {
		if utils.WorkspaceTemplateMatchTargetCluster(&workspaceTemplate, cluster) {
			result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: workspaceTemplate.Name}})
		}
	}
	return result
}

// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=workspacerolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces,verbs=get;list;watch;

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("workspacetemplate", req.NamespacedName)
	workspaceTemplate := &tenantv1alpha2.WorkspaceTemplate{}
	if err := r.Get(ctx, req.NamespacedName, workspaceTemplate); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctx = klog.NewContext(ctx, logger)
	if workspaceTemplate.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		if !controllerutil.ContainsFinalizer(workspaceTemplate, workspaceTemplateFinalizer) {
			updated := workspaceTemplate.DeepCopy()
			controllerutil.AddFinalizer(updated, workspaceTemplateFinalizer)
			return ctrl.Result{}, r.Patch(ctx, updated, client.MergeFrom(workspaceTemplate))
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(workspaceTemplate, workspaceTemplateFinalizer) ||
			controllerutil.ContainsFinalizer(workspaceTemplate, orphanFinalizer) {
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(workspaceTemplate, workspaceTemplateFinalizer)
			controllerutil.RemoveFinalizer(workspaceTemplate, orphanFinalizer)
			if err := r.Update(ctx, workspaceTemplate); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	if err := r.initWorkspaceRoles(ctx, workspaceTemplate); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.initManagerRoleBinding(ctx, workspaceTemplate); err != nil {
		return ctrl.Result{}, err
	}

	if r.ClusterClientSet != nil {
		if err := r.sync(ctx, workspaceTemplate); err != nil {
			return ctrl.Result{}, err
		}
	}

	r.recorder.Event(workspaceTemplate, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) sync(ctx context.Context, workspaceTemplate *tenantv1alpha2.WorkspaceTemplate) error {
	clusters, err := r.ClusterClientSet.ListCluster(ctx)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		// skip if cluster is not ready
		if !clusterutils.IsClusterReady(&cluster) {
			klog.FromContext(ctx).V(4).Info("skip multi-cluster sync, cluster is not ready", "cluster", cluster.Name)
			continue
		}
		if err := r.syncWorkspaceTemplate(ctx, cluster, workspaceTemplate); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncWorkspaceTemplate(ctx context.Context, cluster clusterv1alpha1.Cluster, workspaceTemplate *tenantv1alpha2.WorkspaceTemplate) error {
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	if utils.WorkspaceTemplateMatchTargetCluster(workspaceTemplate, &cluster) {
		workspace := &tenantv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: workspaceTemplate.Name}}

		op, err := controllerutil.CreateOrUpdate(ctx, clusterClient, workspace, func() error {
			workspace.Labels = workspaceTemplate.Spec.Template.Labels
			workspace.Annotations = workspaceTemplate.Spec.Template.Annotations
			workspace.Spec = workspaceTemplate.Spec.Template.Spec
			return nil
		})

		if err != nil {
			return err
		}

		klog.FromContext(ctx).V(4).Info("workspace successfully synced", "operation", op)
	} else {
		orphan := metav1.DeletePropagationOrphan
		err = clusterClient.Delete(ctx, &tenantv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: workspaceTemplate.Name}},
			&client.DeleteOptions{PropagationPolicy: &orphan})
		return client.IgnoreNotFound(err)
	}

	return nil
}

func (r *Reconciler) initWorkspaceRoles(ctx context.Context, workspaceTemplate *tenantv1alpha2.WorkspaceTemplate) error {
	logger := klog.FromContext(ctx)
	var templates iamv1beta1.RoleBaseList
	// scope.iam.kubesphere.io/workspace: ""
	if err := r.List(ctx, &templates, client.MatchingLabels{fmt.Sprintf(iamv1beta1.ScopeLabelFormat, iamv1beta1.ScopeWorkspace): ""}); err != nil {
		return err
	}
	for _, template := range templates.Items {
		var builtinWorkspaceRoleTemplate iamv1beta1.WorkspaceRole
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(template.Role.Raw), 1024).Decode(&builtinWorkspaceRoleTemplate); err == nil &&
			builtinWorkspaceRoleTemplate.Kind == iamv1beta1.ResourceKindWorkspaceRole {
			existingWorkspaceRole := &iamv1beta1.WorkspaceRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s-%s", workspaceTemplate.Name, builtinWorkspaceRoleTemplate.Name),
				},
			}
			op, err := controllerutil.CreateOrUpdate(ctx, r.Client, existingWorkspaceRole, func() error {
				existingWorkspaceRole.Labels = builtinWorkspaceRoleTemplate.Labels
				if existingWorkspaceRole.Labels == nil {
					existingWorkspaceRole.Labels = make(map[string]string)
				}
				existingWorkspaceRole.Labels[tenantv1alpha1.WorkspaceLabel] = workspaceTemplate.Name
				existingWorkspaceRole.Annotations = builtinWorkspaceRoleTemplate.Annotations
				existingWorkspaceRole.AggregationRoleTemplates = builtinWorkspaceRoleTemplate.AggregationRoleTemplates
				existingWorkspaceRole.Rules = builtinWorkspaceRoleTemplate.Rules
				return nil
			})
			if err != nil {
				return err
			}
			logger.V(4).Info("builtin workspace role successfully initialized", "operation", op)
		} else if err != nil {
			logger.Error(err, "invalid builtin workspace role found", "name", template.Name)
		}
	}
	return nil
}

func (r *Reconciler) initManagerRoleBinding(ctx context.Context, workspaceTemplate *tenantv1alpha2.WorkspaceTemplate) error {
	manager := workspaceTemplate.Spec.Template.Spec.Manager
	if manager == "" {
		return nil
	}

	var user iamv1beta1.User
	if err := r.Get(ctx, types.NamespacedName{Name: manager}, &user); err != nil {
		return client.IgnoreNotFound(err)
	}

	// skip if user has been deleted
	if !user.DeletionTimestamp.IsZero() {
		return nil
	}

	workspaceAdminRoleName := fmt.Sprintf("%s-admin", workspaceTemplate.Name)
	existWorkspaceRoleBinding := &iamv1beta1.WorkspaceRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: workspaceAdminRoleName}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, existWorkspaceRoleBinding, func() error {
		existWorkspaceRoleBinding.Labels = map[string]string{
			tenantv1alpha1.WorkspaceLabel: workspaceTemplate.Name,
			iamv1beta1.UserReferenceLabel: manager,
			iamv1beta1.RoleReferenceLabel: workspaceAdminRoleName,
		}

		existWorkspaceRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindWorkspaceRole,
			Name:     workspaceAdminRoleName,
		}
		existWorkspaceRoleBinding.Subjects = []rbacv1.Subject{
			{
				Name:     manager,
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			},
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}
