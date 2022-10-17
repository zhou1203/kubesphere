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
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/getter"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	"kubesphere.io/utils/helm"

	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	SubscriptionFinalizer = "subscriptions.kubesphere.io"
)

var _ reconcile.Reconciler = &SubscriptionReconciler{}

type SubscriptionReconciler struct {
	client.Client
}

// reconcileDelete delete the helm release involved and remove finalizer from subscription.
func (r *SubscriptionReconciler) reconcileDelete(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	helmExecutor, err := helm.NewExecutor(sub.Status.TargetNamespace, sub.Status.ReleaseName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if sub.Status.JobName != "" {
		job := &batchv1.Job{}
		if err = r.Get(ctx, client.ObjectKey{Namespace: sub.Status.TargetNamespace, Name: sub.Status.JobName}, job); err != nil {
			return ctrl.Result{}, err
		}
		if job.Status.Succeeded > 0 {
			klog.V(4).Infof("remove the finalizer for subscription %s", sub.Name)
			return r.removeFinalizer(ctx, sub)
		}
		return ctrl.Result{
			RequeueAfter: time.Second * 3,
		}, nil
	}

	if _, err = helmExecutor.Manifest(); err != nil {
		if !strings.Contains(err.Error(), "release: not found") {
			return ctrl.Result{}, err
		}
		// The involved release does not exist, just move on.
		return r.removeFinalizer(ctx, sub)
	}

	jobName, err := helmExecutor.Uninstall(ctx)
	if err != nil {
		klog.Errorf("delete helm release %s/%s failed, error: %s", sub.Status.TargetNamespace, sub.Status.ReleaseName, err)
		return ctrl.Result{}, err
	}

	klog.Infof("delete helm release %s/%s", sub.Status.TargetNamespace, sub.Status.ReleaseName)
	sub.Status.JobName = jobName
	if err = r.Update(ctx, sub); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) removeFinalizer(ctx context.Context, sub *corev1alpha1.Subscription) (ctrl.Result, error) {
	// Remove the finalizer from the subscription and update it.
	controllerutil.RemoveFinalizer(sub, SubscriptionFinalizer)
	if err := r.Update(ctx, sub); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) loadChartData(ctx context.Context, ref *corev1alpha1.ExtensionRef) ([]byte, error) {
	extensionVersion := &corev1alpha1.ExtensionVersion{}
	err := r.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-%s", ref.Name, ref.Version)}, extensionVersion)
	if err != nil {
		return nil, err
	}
	repo := &corev1alpha1.Repository{}
	err = r.Get(ctx, types.NamespacedName{Name: extensionVersion.Spec.Repository}, repo)
	if err != nil {
		return nil, err
	}
	po := &corev1.Pod{}
	podName := generatePodName(repo.Name)
	if err := r.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: podName}, po); err != nil {
		return nil, err
	}

	url := strings.TrimPrefix(extensionVersion.Spec.ChartURL, "/")
	if len(url) == 0 {
		return nil, fmt.Errorf("empty url")
	}

	// TODO: Fetch load data from repo service.
	if po.Status.Phase == corev1.PodRunning {
		g, _ := getter.NewHTTPGetter()
		buf, err := g.Get(fmt.Sprintf("http://%s:8080/%s", po.Status.PodIP, url),
			getter.WithTimeout(5*time.Minute),
		)
		if err != nil {
			return nil, err
		} else {
			return buf.Bytes(), nil
		}
	} else {
		return nil, fmt.Errorf("repo not ready")
	}
}

func (r *SubscriptionReconciler) doReconcile(ctx context.Context, sub *corev1alpha1.Subscription) (*corev1alpha1.Subscription, ctrl.Result, error) {

	// TODO: reconsider how to define the target namespace
	targetNamespace := fmt.Sprintf("extension-%s", sub.Spec.Extension.Name)
	releaseName := sub.Spec.Extension.Name

	helmExecutor, err := helm.NewExecutor(targetNamespace, releaseName)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var jobName string
	if _, err = helmExecutor.Manifest(); err != nil {
		if !strings.Contains(err.Error(), "release: not found") {
			return sub, ctrl.Result{}, err
		} else {
			charData, err := r.loadChartData(ctx, &sub.Spec.Extension)
			if err == nil {
				jobName, err = helmExecutor.Install(ctx, sub.Spec.Extension.Name, charData, []byte(sub.Spec.Config))
				if err != nil {
					klog.Errorf("install helm release %s/%s failed, error: %s", targetNamespace, releaseName, err)
					return sub, ctrl.Result{}, err
				} else {
					klog.Infof("install helm release %s/%s", targetNamespace, releaseName)
				}
			} else {
				klog.Errorf("fail to load chart data for subscription: %s, error: %s", sub.Name, err)
				return nil, ctrl.Result{}, err
			}
		}
	} else { //nolint:staticcheck
		// TODO: Upgrade the release.
	}

	// TODO: Add more conditions
	sub.Status = corev1alpha1.SubscriptionStatus{
		State:           corev1alpha1.StateInstalling,
		ReleaseName:     releaseName,
		TargetNamespace: targetNamespace,
		JobName:         jobName,
	}
	if err := r.Update(ctx, sub); err != nil {
		return sub, ctrl.Result{}, err
	}

	return sub, ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(4).Infof("sync subscription: %s ", req.String())

	sub := &corev1alpha1.Subscription{}
	if err := r.Client.Get(ctx, req.NamespacedName, sub); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(sub, SubscriptionFinalizer) {
		patch := client.MergeFrom(sub.DeepCopy())
		controllerutil.AddFinalizer(sub, SubscriptionFinalizer)
		if err := r.Patch(ctx, sub, patch); err != nil {
			klog.Errorf("unable to register finalizer for subscription %s, error: %s", sub.Name, err)
			return ctrl.Result{}, err
		}
	}

	if sub.ObjectMeta.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, sub)
	}

	if _, res, err := r.doReconcile(ctx, sub); err != nil {
		return res, err
	}

	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		Named("subscription-controller").
		For(&corev1alpha1.Subscription{}).Complete(r)
}
