/*
Copyright 2020 The KubeSphere Authors.

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

package group

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"kubesphere.io/kubesphere/pkg/controller"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/record"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1beta1 "kubesphere.io/api/tenant/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	controllerName = "group"
	finalizer      = "finalizers.kubesphere.io/groups"
)

type Reconciler struct {
	client.Client

	recorder record.EventRecorder
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.Client = mgr.GetClient()
	return builder.
		ControllerManagedBy(mgr).
		For(
			&iamv1beta1.Group{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		Named(controllerName).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	group := &iamv1beta1.Group{}
	if err := r.Get(ctx, req.NamespacedName, group); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if group.ObjectMeta.DeletionTimestamp.IsZero() {
		var g *iamv1beta1.Group
		if !sliceutil.HasString(group.Finalizers, finalizer) {
			g = group.DeepCopy()
			g.ObjectMeta.Finalizers = append(g.ObjectMeta.Finalizers, finalizer)
		}

		// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.
		// Ensure not controlled by Kubefed
		if group.Labels == nil || group.Labels[constants.KubefedManagedLabel] != "false" {
			if g == nil {
				g = group.DeepCopy()
			}
			if g.Labels == nil {
				g.Labels = make(map[string]string, 0)
			}
			g.Labels[constants.KubefedManagedLabel] = "false"
		}

		// Set OwnerReferences when the group has a parent or Workspace. And it's not owned by kubefed
		if group.Labels != nil && group.Labels[constants.KubefedManagedLabel] != "true" {
			if parent, ok := group.Labels[iamv1beta1.GroupParent]; ok {
				// If the Group is owned by a Parent
				if !k8sutil.IsControlledBy(group.OwnerReferences, "Group", parent) {
					if g == nil {
						g = group.DeepCopy()
					}
					groupParent := &iamv1beta1.Group{}
					if err := r.Get(ctx, client.ObjectKey{Name: parent}, groupParent); err != nil {
						if errors.IsNotFound(err) {
							utilruntime.HandleError(fmt.Errorf("parent group '%s' no longer exists", req.String()))
							delete(g.Labels, iamv1beta1.GroupParent)
						} else {
							return ctrl.Result{}, err
						}
					} else {
						if err = controllerutil.SetControllerReference(groupParent, g, scheme.Scheme); err != nil {
							return ctrl.Result{}, err
						}
					}
				}
			} else if ws, ok := group.Labels[constants.WorkspaceLabelKey]; ok {
				// If the Group is owned by a Workspace
				if !k8sutil.IsControlledBy(group.OwnerReferences, tenantv1beta1.ResourceKindWorkspaceTemplate, ws) {
					workspace := &tenantv1beta1.WorkspaceTemplate{}
					if err := r.Get(ctx, client.ObjectKey{Name: ws}, workspace); err != nil {
						if errors.IsNotFound(err) {
							utilruntime.HandleError(fmt.Errorf("workspace '%s' no longer exists", ws))
						} else {
							return ctrl.Result{}, err
						}
					} else {
						if g == nil {
							g = group.DeepCopy()
						}
						g.OwnerReferences = k8sutil.RemoveWorkspaceOwnerReference(g.OwnerReferences)
						if err = controllerutil.SetControllerReference(workspace, g, scheme.Scheme); err != nil {
							return ctrl.Result{}, err
						}
					}
				}
			}
		}
		if g != nil {
			return ctrl.Result{}, r.Update(ctx, g)
		}
	} else {
		// The object is being deleted
		if sliceutil.HasString(group.ObjectMeta.Finalizers, finalizer) {
			if err := r.deleteGroupBindings(ctx, group); err != nil {
				return ctrl.Result{}, err
			}

			if err := r.deleteRoleBindings(ctx, group); err != nil {
				return ctrl.Result{}, err
			}

			group.Finalizers = sliceutil.RemoveString(group.ObjectMeta.Finalizers, func(item string) bool {
				return item == finalizer
			})

			return ctrl.Result{}, r.Update(ctx, group)
		}
		return ctrl.Result{}, nil
	}

	// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.

	r.recorder.Event(group, corev1.EventTypeNormal, controller.Synced, controller.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) deleteGroupBindings(ctx context.Context, group *iamv1beta1.Group) error {
	if len(group.Name) > validation.LabelValueMaxLength {
		// ignore invalid label value error
		return nil
	}

	// Groupbindings that created by kubesphere will be deleted directly.
	return r.DeleteAllOf(ctx, &iamv1beta1.GroupBinding{}, client.GracePeriodSeconds(0), client.MatchingLabelsSelector{
		Selector: labels.SelectorFromValidatedSet(labels.Set{iamv1beta1.GroupReferenceLabel: group.Name}),
	})
}

// remove all RoleBindings.
func (r *Reconciler) deleteRoleBindings(ctx context.Context, group *iamv1beta1.Group) error {
	if len(group.Name) > validation.LabelValueMaxLength {
		// ignore invalid label value error
		return nil
	}

	selector := labels.SelectorFromValidatedSet(labels.Set{iamv1beta1.GroupReferenceLabel: group.Name})
	deleteOption := client.GracePeriodSeconds(0)

	if err := r.DeleteAllOf(ctx, &iamv1beta1.WorkspaceRoleBinding{}, deleteOption, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}

	if err := r.DeleteAllOf(ctx, &rbacv1.ClusterRoleBinding{}, deleteOption, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}

	namespaces := &corev1.NamespaceList{}
	if err := r.List(ctx, namespaces); err != nil {
		return err
	}
	for _, namespace := range namespaces.Items {
		if err := r.DeleteAllOf(ctx, &rbacv1.RoleBinding{}, deleteOption, client.MatchingLabelsSelector{Selector: selector}, client.InNamespace(namespace.Name)); err != nil {
			return err
		}
	}
	return nil
}
