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
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	categoryController       = "category-controller"
	countOfRelatedExtensions = "kubesphere.io/count"
)

var _ reconcile.Reconciler = &CategoryReconciler{}

type CategoryReconciler struct {
	client.Client
	recorder record.EventRecorder
	logger   logr.Logger
}

func (r *CategoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("category", req.String())
	logger.V(4).Info("sync category")
	ctx = klog.NewContext(ctx, logger)

	category := &corev1alpha1.Category{}
	if err := r.Client.Get(ctx, req.NamespacedName, category); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	extensions := &corev1alpha1.ExtensionList{}
	if err := r.List(ctx, extensions, client.MatchingLabels{fmt.Sprintf(corev1alpha1.CategoryLabelFormat, category.Name): ""}); err != nil {
		return ctrl.Result{}, err
	}

	total := strconv.Itoa(len(extensions.Items))
	if category.Annotations[countOfRelatedExtensions] != total {
		if category.Annotations == nil {
			category.Annotations = make(map[string]string)
		}
		category.Annotations[countOfRelatedExtensions] = total
		if err := r.Update(ctx, category); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *CategoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(categoryController)
	r.recorder = mgr.GetEventRecorderFor(categoryController)
	ctr, err := ctrl.NewControllerManagedBy(mgr).
		Named(categoryController).
		For(&corev1alpha1.Category{}).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", categoryController, err)
	}

	err = ctr.Watch(&source.Kind{Type: &corev1alpha1.Extension{}},
		handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
			var requests []reconcile.Request
			extension := object.(*corev1alpha1.Extension)
			for k := range extension.Labels {
				if !strings.HasPrefix(k, corev1alpha1.CategoryLabelPrefix) {
					continue
				}
				category := strings.TrimPrefix(k, corev1alpha1.CategoryLabelPrefix+"/")
				if category == "" {
					continue
				}
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: category,
					},
				})
			}
			return requests
		}),
		predicate.LabelChangedPredicate{},
	)
	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", categoryController, err)
	}
	return nil
}
