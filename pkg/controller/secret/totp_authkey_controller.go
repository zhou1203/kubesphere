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

package secret

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/config"
	"kubesphere.io/kubesphere/pkg/constants"
	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	"kubesphere.io/kubesphere/pkg/models/auth"
)

const (
	totpAuthKeyController = "totp-auth-key"
	unbindTOTPAuthKey     = "iam.kubesphere.io/unbind-totp-auth-key"
)

var _ kscontroller.Controller = &TOTPAuthKeyController{}
var _ reconcile.Reconciler = &TOTPAuthKeyController{}

func (r *TOTPAuthKeyController) Name() string {
	return totpAuthKeyController
}

func (r *TOTPAuthKeyController) Enabled(clusterRole string) bool {
	return strings.EqualFold(clusterRole, string(clusterv1alpha1.ClusterRoleHost))
}

// TOTPAuthKeyController reconciles a ServiceAccount object
type TOTPAuthKeyController struct {
	client.Client
	logger   logr.Logger
	recorder record.EventRecorder
	scheme   *runtime.Scheme
}

func (r *TOTPAuthKeyController) SetupWithManager(mgr *kscontroller.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(totpAuthKeyController)
	r.scheme = mgr.GetScheme()
	r.recorder = mgr.GetEventRecorderFor(totpAuthKeyController)

	return ctrl.NewControllerManagedBy(mgr).
		Named(totpAuthKeyController).
		For(&corev1.Secret{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if object.GetNamespace() != constants.KubeSphereNamespace {
				return false
			}
			secret := object.(*corev1.Secret)
			return secret.Type == auth.SecretTypeTOTPAuthKey
		})).
		Complete(r)
}

func (r *TOTPAuthKeyController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("secret", req.NamespacedName)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if secret.Type != auth.SecretTypeTOTPAuthKey {
		return ctrl.Result{}, nil
	}

	if secret.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		if !controllerutil.ContainsFinalizer(secret, unbindTOTPAuthKey) {
			expected := secret.DeepCopy()
			controllerutil.AddFinalizer(expected, unbindTOTPAuthKey)
			return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(secret))
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(secret, unbindTOTPAuthKey) {
			if err := r.unbindTOTPAuthKey(ctx, r.logger, secret); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete related resources: %s", err)
			}
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(secret, unbindTOTPAuthKey)
			if err := r.Update(ctx, secret); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if secret.Labels[config.GenericConfigTypeLabel] != auth.ConfigTypeTOTPAuthKey {
		secret = secret.DeepCopy()
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels[config.GenericConfigTypeLabel] = auth.ConfigTypeTOTPAuthKey
		return ctrl.Result{}, r.Update(ctx, secret)
	}

	key, err := auth.TOTPAuthKeyFrom(secret)
	if err != nil {
		logger.Error(err, "failed to get totp auth key from secret")
		return ctrl.Result{}, nil
	}
	username := secret.Labels[iamv1alpha2.UserReferenceLabel]
	if username != key.AccountName() {
		secret = secret.DeepCopy()
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels[iamv1alpha2.UserReferenceLabel] = key.AccountName()
		return ctrl.Result{}, r.Update(ctx, secret)
	}
	user := &iamv1alpha2.User{}
	if err := r.Get(ctx, types.NamespacedName{Name: username}, user); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !user.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if user.Annotations[auth.TOTPAuthKeyRefAnnotation] != req.NamespacedName.Name {
		user = user.DeepCopy()
		if user.Annotations == nil {
			user.Annotations = make(map[string]string)
		}
		user.Annotations[auth.TOTPAuthKeyRefAnnotation] = req.NamespacedName.Name
		return ctrl.Result{}, r.Update(ctx, user)
	}

	if !metav1.IsControlledBy(secret, user) {
		secret = secret.DeepCopy()
		if err := ctrl.SetControllerReference(user, secret, r.scheme); err != nil {
			r.logger.Error(err, "failed to set controller reference", "secret", secret.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, r.Update(ctx, secret)
	}

	return ctrl.Result{}, nil
}

func (r *TOTPAuthKeyController) unbindTOTPAuthKey(ctx context.Context, logger logr.Logger, secret *corev1.Secret) error {
	users := &iamv1alpha2.UserList{}
	if err := r.List(ctx, users); err != nil {
		return fmt.Errorf("failed to list users: %s", err)
	}
	for _, user := range users.Items {
		if user.Annotations[auth.TOTPAuthKeyRefAnnotation] == secret.Name && user.DeletionTimestamp.IsZero() {
			user := user.DeepCopy()
			delete(user.Annotations, auth.TOTPAuthKeyRefAnnotation)
			if err := r.Update(ctx, user); err != nil {
				logger.Error(err, "failed to unbind totp auth key", "user", user.Name)
				return err
			}
		}
	}
	return nil
}
