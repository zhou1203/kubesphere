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

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
)

const (
	ExtensionVersionFinalizer = "extensionversions.kubesphere.io"
)

var _ reconcile.Reconciler = &ExtensionVersionReconciler{}

type ExtensionVersionReconciler struct {
	client.Client
	K8sVersion string
}

// reconcileDelete delete the extension.
func (r *ExtensionVersionReconciler) reconcileDelete(ctx context.Context, extensionVersion *corev1alpha1.ExtensionVersion) (ctrl.Result, error) {
	klog.V(4).Infof("remove the finalizer from extension version %s", extensionVersion.Name)

	// Remove the finalizer from the extension
	controllerutil.RemoveFinalizer(extensionVersion, ExtensionVersionFinalizer)
	if err := r.Update(ctx, extensionVersion); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ExtensionVersionReconciler) reconcile(ctx context.Context, extensionVersion *corev1alpha1.ExtensionVersion) (ctrl.Result, error) {
	extension := &corev1alpha1.Extension{}
	name := extensionVersion.Labels[corev1alpha1.ExtensionReferenceLabel]
	if err := r.Get(ctx, types.NamespacedName{Name: name}, extension); err != nil {
		// skip extension status sync if not found
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return reconcileExtensionStatus(ctx, r.Client, extension, r.K8sVersion)
}

func (r *ExtensionVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(4).Infof("sync extension version: %s ", req.String())

	extensionVersion := &corev1alpha1.ExtensionVersion{}
	if err := r.Client.Get(ctx, req.NamespacedName, extensionVersion); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(extensionVersion, ExtensionVersionFinalizer) {
		expected := extensionVersion.DeepCopy()
		controllerutil.AddFinalizer(expected, ExtensionVersionFinalizer)
		return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(extensionVersion))
	}

	if extensionVersion.ObjectMeta.DeletionTimestamp != nil {
		if result, err := r.reconcileDelete(ctx, extensionVersion); err != nil {
			return result, err
		}
	}

	if res, err := r.reconcile(ctx, extensionVersion); err != nil {
		return res, err
	}
	return ctrl.Result{}, nil
}

func (r *ExtensionVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("extension-version-controller").
		For(&corev1alpha1.ExtensionVersion{}).Complete(r)
}
