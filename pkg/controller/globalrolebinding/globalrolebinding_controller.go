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

package globalrolebinding

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"kubesphere.io/kubesphere/pkg/constants"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

const controllerName = "globalrolebinding-controller"

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

	ctr, err := builder.
		ControllerManagedBy(mgr).
		For(&iamv1beta1.GlobalRoleBinding{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named(controllerName).
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
	globalRoleBindings := &iamv1beta1.GlobalRoleBindingList{}
	if err := r.List(context.Background(), globalRoleBindings); err != nil {
		r.logger.Error(err, "failed to list global role bindings")
		return []reconcile.Request{}
	}
	var result []reconcile.Request
	for _, globalRoleBinding := range globalRoleBindings.Items {
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Name: globalRoleBinding.Name}})
	}
	return result
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	globalRoleBinding := &iamv1beta1.GlobalRoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, globalRoleBinding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if globalRoleBinding.RoleRef.Name == iamv1beta1.PlatformAdmin {
		if err := r.assignClusterAdminRole(ctx, globalRoleBinding); err != nil {
			return ctrl.Result{}, err
		}
	}

	if r.ClusterClientSet != nil {
		if err := r.sync(ctx, globalRoleBinding); err != nil {
			return ctrl.Result{}, err
		}
	}

	r.recorder.Event(globalRoleBinding, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) assignClusterAdminRole(ctx context.Context, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	username := findExpectedUsername(globalRoleBinding)
	if username == "" {
		return nil
	}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", username, iamv1beta1.ClusterAdmin),
			Labels: map[string]string{iamv1beta1.RoleReferenceLabel: iamv1beta1.ClusterAdmin,
				iamv1beta1.UserReferenceLabel: username},
		},
		Subjects: ensureSubjectAPIVersionIsValid(globalRoleBinding.Subjects),
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     iamv1beta1.ResourceKindClusterRole,
			Name:     iamv1beta1.ClusterAdmin,
		},
	}

	err := controllerutil.SetControllerReference(globalRoleBinding, clusterRoleBinding, scheme.Scheme)
	if err != nil {
		return err
	}
	return client.IgnoreAlreadyExists(r.Create(ctx, clusterRoleBinding))
}

func (r *Reconciler) sync(ctx context.Context, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	clusters, err := r.ClusterClientSet.ListCluster(ctx)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		// skip if cluster is not ready
		if !clusterutils.IsClusterReady(&cluster) {
			continue
		}
		if err := r.syncGlobalRoleBinding(ctx, cluster, globalRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncGlobalRoleBinding(ctx context.Context, cluster clusterv1alpha1.Cluster, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	if r.ClusterClientSet.IsHostCluster(&cluster) {
		return nil
	}
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	target := &iamv1beta1.GlobalRoleBinding{}
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: globalRoleBinding.Name}, target); err != nil {
		if errors.IsNotFound(err) {
			target = globalRoleBinding.DeepCopy()
			return clusterClient.Create(ctx, target)
		}
		return err
	}
	if !reflect.DeepEqual(target.RoleRef, globalRoleBinding.RoleRef) ||
		!reflect.DeepEqual(target.Subjects, globalRoleBinding.Subjects) ||
		!reflect.DeepEqual(target.Labels, globalRoleBinding.Labels) ||
		!reflect.DeepEqual(target.Annotations, globalRoleBinding.Annotations) {
		target.Labels = globalRoleBinding.Labels
		target.Annotations = globalRoleBinding.Annotations
		target.RoleRef = globalRoleBinding.RoleRef
		target.Subjects = globalRoleBinding.Subjects
		return clusterClient.Update(ctx, target)
	}
	return nil
}

func findExpectedUsername(globalRoleBinding *iamv1beta1.GlobalRoleBinding) string {
	for _, subject := range globalRoleBinding.Subjects {
		if subject.Kind == iamv1beta1.ResourceKindUser {
			return subject.Name
		}
	}
	return ""
}

func ensureSubjectAPIVersionIsValid(subjects []rbacv1.Subject) []rbacv1.Subject {
	validSubjects := make([]rbacv1.Subject, 0)
	for _, subject := range subjects {
		if subject.Kind == iamv1beta1.ResourceKindUser {
			validSubject := rbacv1.Subject{
				Kind:     iamv1beta1.ResourceKindUser,
				APIGroup: rbacv1.GroupName,
				Name:     subject.Name,
			}
			validSubjects = append(validSubjects, validSubject)
		}
	}
	return validSubjects
}
