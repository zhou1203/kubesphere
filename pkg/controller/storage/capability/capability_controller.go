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

package capability

import (
	"context"
	"reflect"
	"strconv"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	annotationAllowSnapshot = "storageclass.kubesphere.io/allow-snapshot"
	annotationAllowClone    = "storageclass.kubesphere.io/allow-clone"
	controllerName          = "capability-controller"
)

// This controller is responsible to watch StorageClass and CSIDriver.
// And then update StorageClass CRD resource object to the newest status.

type Reconciler struct {
	client.Client
}

// When creating a new storage class, the controller will create a new storage capability object.
// When updating storage class, the controller will update or create the storage capability object.
// When deleting storage class, the controller will delete storage capability object.

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	storageClass := &storagev1.StorageClass{}
	if err := r.Get(ctx, req.NamespacedName, storageClass); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Cloning and volumeSnapshot support only available for CSI drivers.
	isCSIStorage := r.hasCSIDriver(ctx, storageClass)
	// Annotate storageClass
	storageClassUpdated := storageClass.DeepCopy()
	if isCSIStorage {
		r.updateSnapshotAnnotation(storageClassUpdated, isCSIStorage)
		r.updateCloneVolumeAnnotation(storageClassUpdated, isCSIStorage)
	} else {
		r.removeAnnotations(storageClassUpdated)
	}
	if !reflect.DeepEqual(storageClass, storageClassUpdated) {
		return ctrl.Result{}, r.Update(ctx, storageClassUpdated)
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) hasCSIDriver(ctx context.Context, storageClass *storagev1.StorageClass) bool {
	driver := storageClass.Provisioner
	if driver != "" {
		if err := r.Get(ctx, client.ObjectKey{Name: driver}, &storagev1.CSIDriver{}); err != nil {
			return false
		}
		return true
	}
	return false
}

func (r *Reconciler) updateSnapshotAnnotation(storageClass *storagev1.StorageClass, snapshotAllow bool) {
	if storageClass.Annotations == nil {
		storageClass.Annotations = make(map[string]string)
	}
	if _, err := strconv.ParseBool(storageClass.Annotations[annotationAllowSnapshot]); err != nil {
		storageClass.Annotations[annotationAllowSnapshot] = strconv.FormatBool(snapshotAllow)
	}
}

func (r *Reconciler) updateCloneVolumeAnnotation(storageClass *storagev1.StorageClass, cloneAllow bool) {
	if storageClass.Annotations == nil {
		storageClass.Annotations = make(map[string]string)
	}
	if _, err := strconv.ParseBool(storageClass.Annotations[annotationAllowClone]); err != nil {
		storageClass.Annotations[annotationAllowClone] = strconv.FormatBool(cloneAllow)
	}
}

func (r *Reconciler) removeAnnotations(storageClass *storagev1.StorageClass) {
	delete(storageClass.Annotations, annotationAllowClone)
	delete(storageClass.Annotations, annotationAllowSnapshot)
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return builder.
		ControllerManagedBy(mgr).
		For(
			&storagev1.StorageClass{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		Watches(
			&source.Kind{Type: &storagev1.CSIDriver{}},
			handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
				storageClassList := &storagev1.StorageClassList{}
				if err := r.List(context.Background(), storageClassList); err != nil {
					klog.Errorf("list StorageClass failed: %v", err)
					return nil
				}
				csiDriver := obj.(*storagev1.CSIDriver)
				requests := make([]reconcile.Request, 0)
				for _, storageClass := range storageClassList.Items {
					if storageClass.Provisioner == csiDriver.Name {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name: storageClass.Name,
							},
						})
					}
				}
				return requests
			}),
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
