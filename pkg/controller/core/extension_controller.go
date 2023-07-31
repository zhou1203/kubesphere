/*
Copyright 2022 KubeSphere Authors

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

package core

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	extensionProtection = "kubesphere.io/extension-protection"
	extensionController = "extension-controller"
)

var _ reconcile.Reconciler = &ExtensionReconciler{}

type ExtensionReconciler struct {
	client.Client
	K8sVersion string
}

// reconcileDelete delete the extension.
func (r *ExtensionReconciler) reconcileDelete(ctx context.Context, extension *corev1alpha1.Extension) (ctrl.Result, error) {
	// Remove the finalizer from the extension
	controllerutil.RemoveFinalizer(extension, extensionProtection)
	if err := r.Update(ctx, extension); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func reconcileExtensionStatus(ctx context.Context, c client.Client, extension *corev1alpha1.Extension, k8sVersion string) (ctrl.Result, error) {
	versionList := corev1alpha1.ExtensionVersionList{}
	if err := c.List(ctx, &versionList, client.MatchingLabels{
		corev1alpha1.ExtensionReferenceLabel: extension.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	versions := make([]corev1alpha1.ExtensionVersionInfo, 0, len(versionList.Items))
	for i := range versionList.Items {
		if versionList.Items[i].DeletionTimestamp.IsZero() {
			versions = append(versions, corev1alpha1.ExtensionVersionInfo{
				Version:           versionList.Items[i].Spec.Version,
				CreationTimestamp: versionList.Items[i].CreationTimestamp,
			})
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version < versions[j].Version
	})

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		if recommended, err := getRecommendedExtensionVersion(versionList.Items, k8sVersion); err == nil {
			expected.Status.RecommendedVersion = recommended
		} else {
			klog.V(2).Info(err)
		}
		expected.Status.Versions = versions

		if expected.Status.RecommendedVersion != extension.Status.RecommendedVersion ||
			!reflect.DeepEqual(expected.Status.Versions, extension.Status.Versions) {
			if err := c.Update(ctx, expected); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update extension status: %s", err)
	}

	return ctrl.Result{}, nil
}

func (r *ExtensionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	extension := &corev1alpha1.Extension{}
	if err := r.Client.Get(ctx, req.NamespacedName, extension); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(extension, extensionProtection) {
		expected := extension.DeepCopy()
		controllerutil.AddFinalizer(expected, extensionProtection)
		return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(extension))
	}

	if extension.ObjectMeta.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, extension)
	}

	return reconcileExtensionStatus(ctx, r.Client, extension, r.K8sVersion)
}

func (r *ExtensionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named(extensionController).
		For(&corev1alpha1.Extension{}).Complete(r)
}
