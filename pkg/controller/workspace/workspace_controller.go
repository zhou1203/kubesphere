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

package workspace

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

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
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	controllerName = "workspace-controller"
)

// Reconciler reconciles a Workspace object
type Reconciler struct {
	client.Client
	Logger                  logr.Logger
	Recorder                record.EventRecorder
	MaxConcurrentReconciles int
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Logger.GetSink() == nil {
		r.Logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor(controllerName)
	}
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		For(&tenantv1alpha1.Workspace{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.kubesphere.io,resources=workspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=rolebases,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=workspaceroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iam.kubesphere.io,resources=workspacerolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.WithValues("workspace", req.NamespacedName)
	rootCtx := context.Background()
	workspace := &tenantv1alpha1.Workspace{}
	if err := r.Get(rootCtx, req.NamespacedName, workspace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// name of your custom finalizer
	finalizer := "finalizers.tenant.kubesphere.io"

	if workspace.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		if !sliceutil.HasString(workspace.ObjectMeta.Finalizers, finalizer) {
			workspace.ObjectMeta.Finalizers = append(workspace.ObjectMeta.Finalizers, finalizer)
			if err := r.Update(rootCtx, workspace); err != nil {
				return ctrl.Result{}, err
			}
			workspaceOperation.WithLabelValues("create", workspace.Name).Inc()
		}
	} else {
		// The object is being deleted
		if sliceutil.HasString(workspace.ObjectMeta.Finalizers, finalizer) {
			// remove our finalizer from the list and update it.
			workspace.ObjectMeta.Finalizers = sliceutil.RemoveString(workspace.ObjectMeta.Finalizers, func(item string) bool {
				return item == finalizer
			})
			logger.V(4).Info("update workspace")
			if err := r.Update(rootCtx, workspace); err != nil {
				logger.Error(err, "update workspace failed")
				return ctrl.Result{}, err
			}
			workspaceOperation.WithLabelValues("delete", workspace.Name).Inc()
		}
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	if err := r.initWorkspaceRoles(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.initManagerRoleBinding(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}

	var namespaces corev1.NamespaceList
	if err := r.List(rootCtx, &namespaces, client.MatchingLabels{tenantv1alpha1.WorkspaceLabel: req.Name}); err != nil {
		logger.Error(err, "list namespaces failed")
		return ctrl.Result{}, err
	} else {
		for _, namespace := range namespaces.Items {
			if err := r.bindWorkspace(rootCtx, logger, &namespace, workspace); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	r.Recorder.Event(workspace, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) bindWorkspace(ctx context.Context, logger logr.Logger, namespace *corev1.Namespace, workspace *tenantv1alpha1.Workspace) error {
	// owner reference not match workspace label
	if !metav1.IsControlledBy(namespace, workspace) {
		namespace := namespace.DeepCopy()
		namespace.OwnerReferences = k8sutil.RemoveWorkspaceOwnerReference(namespace.OwnerReferences)
		if err := controllerutil.SetControllerReference(workspace, namespace, scheme.Scheme); err != nil {
			logger.Error(err, "set controller reference failed")
			return err
		}
		logger.V(4).Info("update namespace owner reference", "workspace", workspace.Name)
		if err := r.Update(ctx, namespace); err != nil {
			logger.Error(err, "update namespace failed")
			return err
		}
	}
	return nil
}

func (r *Reconciler) initWorkspaceRoles(ctx context.Context, workspace *tenantv1alpha1.Workspace) error {
	logger := klog.FromContext(ctx)
	var templates iamv1beta1.RoleBaseList
	if err := r.List(ctx, &templates); err != nil {
		logger.Error(err, "list role base failed")
		return err
	}
	for _, template := range templates.Items {
		var expected iamv1beta1.WorkspaceRole
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(template.Role.Raw), 1024).Decode(&expected); err == nil && expected.Kind == iamv1beta1.ResourceKindWorkspaceRole {
			expected.Name = fmt.Sprintf("%s-%s", workspace.Name, expected.Name)
			if expected.Labels == nil {
				expected.Labels = make(map[string]string)
			}
			expected.Labels[tenantv1alpha1.WorkspaceLabel] = workspace.Name
			workspaceRole := &iamv1beta1.WorkspaceRole{}
			if err := r.Get(ctx, types.NamespacedName{Name: expected.Name}, workspaceRole); err != nil {
				if errors.IsNotFound(err) {
					logger.V(4).Info("create workspace role", "workspacerole", expected.Name)
					if err := r.Create(ctx, &expected); err != nil {
						logger.Error(err, "create workspace role failed")
						return err
					}
					continue
				} else {
					logger.Error(err, "get workspace role failed")
					return err
				}
			}
			if !reflect.DeepEqual(expected.Labels, workspaceRole.Labels) ||
				!reflect.DeepEqual(expected.Annotations, workspaceRole.Annotations) ||
				!reflect.DeepEqual(expected.Rules, workspaceRole.Rules) {
				workspaceRole.Labels = expected.Labels
				workspaceRole.Annotations = expected.Annotations
				workspaceRole.Rules = expected.Rules
				logger.V(4).Info("update workspace role", "workspacerole", workspaceRole.Name)
				if err := r.Update(ctx, workspaceRole); err != nil {
					logger.Error(err, "update workspace role failed")
					return err
				}
			}
		} else if err != nil {
			logger.Error(fmt.Errorf("invalid role base found"), "init workspace roles failed", "name", template.Name)
		}
	}
	return nil
}

func (r *Reconciler) initManagerRoleBinding(ctx context.Context, workspace *tenantv1alpha1.Workspace) error {
	logger := klog.FromContext(ctx)
	manager := workspace.Spec.Manager
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

	workspaceAdminRoleName := fmt.Sprintf("%s-admin", workspace.Name)
	managerRoleBinding := &iamv1beta1.WorkspaceRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: workspaceAdminRoleName,
		},
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, managerRoleBinding, workspaceRoleBindingChanger(managerRoleBinding, workspace.Name, manager, workspaceAdminRoleName)); err != nil {
		logger.Error(err, "create workspace manager role binding failed")
		return err
	}

	return nil
}

func workspaceRoleBindingChanger(workspaceRoleBinding *iamv1beta1.WorkspaceRoleBinding, workspace, username, workspaceRoleName string) controllerutil.MutateFn {
	return func() error {
		workspaceRoleBinding.Labels = map[string]string{
			tenantv1alpha1.WorkspaceLabel: workspace,
			iamv1beta1.UserReferenceLabel: username,
			iamv1beta1.RoleReferenceLabel: workspaceRoleName,
		}

		workspaceRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			Kind:     iamv1beta1.ResourceKindWorkspaceRole,
			Name:     workspaceRoleName,
		}

		workspaceRoleBinding.Subjects = []rbacv1.Subject{
			{
				Name:     username,
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
			},
		}
		return nil
	}
}
