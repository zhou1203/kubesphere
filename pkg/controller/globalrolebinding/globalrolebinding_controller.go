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

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
)

const controllerName = "globalrolebinding-controller"

type Reconciler struct {
	client.Client

	recorder record.EventRecorder
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

	// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.
	// if err = c.multiClusterSync(globalRoleBinding); err != nil {
	// 	klog.Error(err)
	// 	return err
	// }

	r.recorder.Event(globalRoleBinding, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)
	return ctrl.Result{}, nil
}

func (r *Reconciler) assignClusterAdminRole(ctx context.Context, globalRoleBinding *iamv1beta1.GlobalRoleBinding) error {
	username := findExpectUsername(globalRoleBinding)
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
			APIGroup: iamv1beta1.GroupName,
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

func findExpectUsername(globalRoleBinding *iamv1beta1.GlobalRoleBinding) string {
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
				APIGroup: iamv1beta1.SchemeGroupVersion.Group,
				Name:     subject.Name,
			}
			validSubjects = append(validSubjects, validSubject)
		}
	}
	return validSubjects
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
			&iamv1beta1.GlobalRoleBinding{},
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
