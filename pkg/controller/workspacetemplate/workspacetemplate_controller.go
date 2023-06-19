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
	"context"
	"reflect"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
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
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
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
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: workspaceTemplate.Name}})
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
		if !sliceutil.HasString(workspaceTemplate.ObjectMeta.Finalizers, workspaceTemplateFinalizer) {
			workspaceTemplate.ObjectMeta.Finalizers = append(workspaceTemplate.ObjectMeta.Finalizers, workspaceTemplateFinalizer)
			if err := r.Update(ctx, workspaceTemplate); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(workspaceTemplate, workspaceTemplateFinalizer) ||
			controllerutil.ContainsFinalizer(workspaceTemplate, orphanFinalizer) {
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(workspaceTemplate, workspaceTemplateFinalizer)
			controllerutil.RemoveFinalizer(workspaceTemplate, orphanFinalizer)
			logger.V(4).Info("update workspace template")
			if err := r.Update(ctx, workspaceTemplate); err != nil {
				logger.Error(err, "update workspace template failed")
				return ctrl.Result{}, err
			}
		}
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
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
			continue
		}
		if err := r.syncWorkspaceTemplate(ctx, cluster, workspaceTemplate); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncWorkspaceTemplate(ctx context.Context, cluster clusterv1alpha1.Cluster, template *tenantv1alpha2.WorkspaceTemplate) error {
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	expected := false
	if len(template.Spec.Placement.Clusters) > 0 {
		for _, clusterRef := range template.Spec.Placement.Clusters {
			if clusterRef.Name == cluster.Name {
				expected = true
				break
			}
		}
	} else if template.Spec.Placement.ClusterSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(template.Spec.Placement.ClusterSelector)
		if err != nil {
			return err
		}
		expected = selector.Matches(labels.Set(cluster.Labels))
	}

	if !expected {
		orphan := metav1.DeletePropagationOrphan
		err = clusterClient.Delete(ctx, &tenantv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: template.Name}},
			&client.DeleteOptions{PropagationPolicy: &orphan})
		return client.IgnoreNotFound(err)
	} else {
		workspace := &tenantv1alpha1.Workspace{}
		if err := clusterClient.Get(ctx, types.NamespacedName{Name: template.Name}, workspace); err != nil {
			if errors.IsNotFound(err) {
				workspace = &tenantv1alpha1.Workspace{
					ObjectMeta: template.Spec.Template.ObjectMeta,
					Spec:       template.Spec.Template.Spec,
				}
				workspace.Name = template.Name
				return clusterClient.Create(ctx, workspace)
			}
			return err
		}
		if !reflect.DeepEqual(workspace.Spec, template.Spec.Template.Spec) ||
			!reflect.DeepEqual(workspace.ObjectMeta.Labels, template.Spec.Template.ObjectMeta.Labels) ||
			!reflect.DeepEqual(workspace.ObjectMeta.Annotations, template.Spec.Template.ObjectMeta.Annotations) {
			workspace.Labels = template.Spec.Template.Labels
			workspace.Annotations = template.Spec.Template.Annotations
			workspace.Spec = template.Spec.Template.Spec
			return clusterClient.Update(ctx, workspace)
		}
	}

	return nil
}
