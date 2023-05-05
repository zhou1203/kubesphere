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

package groupbinding

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	controllerName = "groupbinding-controller"
	finalizer      = "finalizers.kubesphere.io/groupsbindings"
)

type Reconciler struct {
	client.Client

	recorder record.EventRecorder
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	groupBinding := &iamv1alpha2.GroupBinding{}
	if err := r.Get(ctx, req.NamespacedName, groupBinding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if groupBinding.ObjectMeta.DeletionTimestamp.IsZero() {
		var g *iamv1alpha2.GroupBinding
		if !sliceutil.HasString(groupBinding.Finalizers, finalizer) {
			g = groupBinding.DeepCopy()
			g.ObjectMeta.Finalizers = append(g.ObjectMeta.Finalizers, finalizer)
		}

		// Ensure not controlled by Kubefed
		// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.
		if groupBinding.Labels == nil || groupBinding.Labels[constants.KubefedManagedLabel] != "false" {
			if g == nil {
				g = groupBinding.DeepCopy()
			}
			if g.Labels == nil {
				g.Labels = make(map[string]string, 0)
			}
			g.Labels[constants.KubefedManagedLabel] = "false"
		}

		if g != nil {
			return ctrl.Result{}, r.Update(ctx, g)
		}
	} else {
		// The object is being deleted
		if sliceutil.HasString(groupBinding.ObjectMeta.Finalizers, finalizer) {
			if err := r.unbindUser(ctx, groupBinding); err != nil {
				return ctrl.Result{}, err
			}

			groupBinding.Finalizers = sliceutil.RemoveString(groupBinding.ObjectMeta.Finalizers, func(item string) bool {
				return item == finalizer
			})

			return ctrl.Result{}, r.Update(ctx, groupBinding)
		}
		return ctrl.Result{}, nil
	}

	if err := r.bindUser(ctx, groupBinding); err != nil {
		return ctrl.Result{}, err
	}

	// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.

	r.recorder.Event(groupBinding, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) unbindUser(ctx context.Context, groupBinding *iamv1alpha2.GroupBinding) error {
	return r.updateUserGroups(ctx, groupBinding, func(groups []string, group string) (bool, []string) {
		// remove a group from the groups
		if sliceutil.HasString(groups, group) {
			groups := sliceutil.RemoveString(groups, func(item string) bool {
				return item == group
			})
			return true, groups
		}
		return false, groups
	})
}

func (r *Reconciler) bindUser(ctx context.Context, groupBinding *iamv1alpha2.GroupBinding) error {
	return r.updateUserGroups(ctx, groupBinding, func(groups []string, group string) (bool, []string) {
		// add group to the groups
		if !sliceutil.HasString(groups, group) {
			groups := append(groups, group)
			return true, groups
		}
		return false, groups
	})
}

// Udpate user's Group property. So no need to query user's groups when authorizing.
func (r *Reconciler) updateUserGroups(ctx context.Context, groupBinding *iamv1alpha2.GroupBinding, operator func(groups []string, group string) (bool, []string)) error {
	for _, u := range groupBinding.Users {
		// Ignore the user if the user being deleted.
		user := &iamv1alpha2.User{}
		if err := r.Get(ctx, client.ObjectKey{Name: u}, user); err != nil {
			if errors.IsNotFound(err) {
				klog.Infof("user %s doesn't exist any more", u)
				continue
			}
			return err
		}

		if !user.DeletionTimestamp.IsZero() {
			continue
		}

		if changed, groups := operator(user.Spec.Groups, groupBinding.GroupRef.Name); changed {
			if err := r.patchUser(ctx, user, groups); err != nil {
				if errors.IsNotFound(err) {
					klog.Infof("user %s doesn't exist any more", u)
					continue
				}
				klog.Error(err)
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) patchUser(ctx context.Context, user *iamv1alpha2.User, groups []string) error {
	newUser := user.DeepCopy()
	newUser.Spec.Groups = groups
	patch := client.MergeFrom(user)
	return r.Patch(ctx, newUser, patch)
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor(controllerName)

	return builder.
		ControllerManagedBy(mgr).
		For(
			&iamv1alpha2.GroupBinding{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Named(controllerName).
		Complete(r)
}
