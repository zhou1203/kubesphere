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

package namespace

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
)

const (
	controllerName = "namespace-controller"
	finalizer      = "finalizers.kubesphere.io/namespaces"
)

// Reconciler reconciles a Namespace object
type Reconciler struct {
	client.Client
	logger   logr.Logger
	recorder record.EventRecorder
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		For(&corev1.Namespace{}).
		Complete(r)
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=rolebases,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("namespace", req.NamespacedName)
	ctx = klog.NewContext(ctx, logger)
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, namespace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if namespace.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		if !controllerutil.ContainsFinalizer(namespace, finalizer) {
			if err := r.initCreatorRoleBinding(ctx, namespace); err != nil {
				return ctrl.Result{}, err
			}
			updated := namespace.DeepCopy()
			controllerutil.AddFinalizer(updated, finalizer)
			return ctrl.Result{}, r.Patch(ctx, updated, client.MergeFrom(namespace))
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(namespace, finalizer) {
			controllerutil.RemoveFinalizer(namespace, finalizer)
			if err := r.Update(ctx, namespace); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	if err := r.initRoles(ctx, namespace); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileWorkspaceOwnerReference(ctx, namespace); err != nil {
		return ctrl.Result{}, err
	}

	r.recorder.Event(namespace, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) reconcileWorkspaceOwnerReference(ctx context.Context, namespace *corev1.Namespace) error {
	workspaceName, hasWorkspaceLabel := namespace.Labels[tenantv1alpha1.WorkspaceLabel]

	if !hasWorkspaceLabel {
		if k8sutil.IsControlledBy(namespace.OwnerReferences, tenantv1alpha1.ResourceKindWorkspace, workspaceName) {
			namespace.OwnerReferences = k8sutil.RemoveWorkspaceOwnerReference(namespace.OwnerReferences)
			return r.Update(ctx, namespace)
		}
		// noting to do
		return nil
	}

	workspace := &tenantv1alpha1.Workspace{}
	if err := r.Get(ctx, types.NamespacedName{Name: workspaceName}, workspace); err != nil {
		owner := metav1.GetControllerOf(namespace)
		if errors.IsNotFound(err) && owner != nil && owner.Kind == tenantv1alpha1.ResourceKindWorkspace {
			namespace.OwnerReferences = k8sutil.RemoveWorkspaceOwnerReference(namespace.OwnerReferences)
			return r.Update(ctx, namespace)
		}
		return client.IgnoreNotFound(err)
	}

	// workspace has been deleted
	if !workspace.ObjectMeta.DeletionTimestamp.IsZero() {
		return nil
	}

	if !metav1.IsControlledBy(namespace, workspace) {
		namespace = namespace.DeepCopy()
		if err := controllerutil.SetControllerReference(workspace, namespace, scheme.Scheme); err != nil {
			return err
		}
		if err := r.Update(ctx, namespace); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) initRoles(ctx context.Context, namespace *corev1.Namespace) error {
	if namespace.Annotations[constants.CreatorAnnotationKey] == "" {
		return nil
	}

	logger := klog.FromContext(ctx)

	var templates iamv1beta1.RoleBaseList
	// scope.iam.kubesphere.io/xxxxx: ""
	matchingLabels := client.MatchingLabels{fmt.Sprintf(iamv1beta1.ScopeLabelFormat, iamv1beta1.ScopeNamespace): ""}
	for annoK, annoV := range namespace.Annotations {
		if strings.HasPrefix(annoK, iamv1beta1.ScopeLabelPrefix) && annoV == "" {
			matchingLabels = client.MatchingLabels{annoK: annoV}
			break
		}
	}
	if err := r.List(ctx, &templates, matchingLabels); err != nil {
		return err
	}
	for _, template := range templates.Items {
		var builtinRoleTemplate iamv1beta1.Role
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(template.Role.Raw), 1024).Decode(&builtinRoleTemplate); err == nil &&
			builtinRoleTemplate.Kind == iamv1beta1.ResourceKindRole {
			existingRole := &iamv1beta1.Role{ObjectMeta: metav1.ObjectMeta{Name: builtinRoleTemplate.Name, Namespace: namespace.Name}}
			op, err := controllerutil.CreateOrUpdate(ctx, r.Client, existingRole, func() error {
				existingRole.Labels = builtinRoleTemplate.Labels
				existingRole.Annotations = builtinRoleTemplate.Annotations
				existingRole.AggregationRoleTemplates = builtinRoleTemplate.AggregationRoleTemplates
				existingRole.Rules = builtinRoleTemplate.Rules
				return nil
			})
			if err != nil {
				return err
			}
			logger.V(4).Info("builtin role successfully initialized", "operation", op)
		} else if err != nil {
			logger.Error(err, "invalid builtin role found", "name", template.Name)
		}
	}
	return nil
}

func (r *Reconciler) initCreatorRoleBinding(ctx context.Context, namespace *corev1.Namespace) error {
	creator := namespace.Annotations[constants.CreatorAnnotationKey]
	if creator == "" {
		return nil
	}
	roleBinding := &iamv1beta1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", creator, iamv1beta1.NamespaceAdmin),
			Namespace: namespace.Name,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, roleBinding, func() error {
		roleBinding.Labels = map[string]string{
			iamv1beta1.UserReferenceLabel: creator,
			iamv1beta1.RoleReferenceLabel: iamv1beta1.NamespaceAdmin,
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: iamv1beta1.GroupName,
			Kind:     iamv1beta1.ResourceKindRole,
			Name:     iamv1beta1.NamespaceAdmin,
		}
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Name:     creator,
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.GroupName,
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	klog.FromContext(ctx).V(4).Info("creator role binding successfully initialized", "operation", op)
	return nil
}
