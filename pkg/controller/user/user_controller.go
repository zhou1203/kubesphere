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

package user

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"

	"k8s.io/apimachinery/pkg/util/validation"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	successSynced = "Synced"
	failedSynced  = "FailedSync"
	// is synced successfully
	messageResourceSynced = "User synced successfully"
	controllerName        = "user-controller"
	// user finalizer
	finalizer       = "finalizers.kubesphere.io/users"
	interval        = time.Second
	timeout         = 15 * time.Second
	syncFailMessage = "Failed to sync: %s"
)

// Reconciler reconciles a User object
type Reconciler struct {
	client.Client
	KubeconfigClient        kubeconfig.Interface
	AuthenticationOptions   *authentication.Options
	Logger                  logr.Logger
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	MaxConcurrentReconciles int
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Logger.GetSink() == nil {
		r.Logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	}
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor(controllerName)
	}
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		For(&iamv1alpha2.User{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.Logger.WithValues("user", req.NamespacedName)
	user := &iamv1alpha2.User{}
	err := r.Get(ctx, req.NamespacedName, user)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object.
		if !sliceutil.HasString(user.Finalizers, finalizer) {
			user.ObjectMeta.Finalizers = append(user.ObjectMeta.Finalizers, finalizer)
			if err = r.Update(ctx, user, &client.UpdateOptions{}); err != nil {
				logger.Error(err, "failed to update user")
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if sliceutil.HasString(user.ObjectMeta.Finalizers, finalizer) {
			if err = r.deleteRoleBindings(ctx, user); err != nil {
				r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			if err = r.deleteGroupBindings(ctx, user); err != nil {
				r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			if err = r.deleteLoginRecords(ctx, user); err != nil {
				r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			user.Finalizers = sliceutil.RemoveString(user.ObjectMeta.Finalizers, func(item string) bool {
				return item == finalizer
			})

			if err = r.Update(ctx, user, &client.UpdateOptions{}); err != nil {
				klog.Error(err)
				r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}
		}

		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, err
	}

	// synchronization through kubefed-controller when multi cluster is enabled
	// TODO: sync logic needs to be updated and no longer relies on KubeFed, it needs to be synchronized manually.
	// if err = r.multiClusterSync(ctx, user); err != nil {
	// 	r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
	// 	return ctrl.Result{}, err
	// }

	// update user status if not managed by kubefed
	managedByKubefed := user.Labels[constants.KubefedManagedLabel] == "true"
	if !managedByKubefed {
		if err = r.encryptPassword(ctx, user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
		if err = r.syncUserStatus(ctx, user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
	}

	if r.KubeconfigClient != nil {
		// ensure user KubeconfigClient configmap is created
		if err = r.KubeconfigClient.CreateKubeConfig(user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, failedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
	}

	r.Recorder.Event(user, corev1.EventTypeNormal, successSynced, messageResourceSynced)

	// block user for AuthenticateRateLimiterDuration duration, after that put it back to the queue to unblock
	if user.Status.State == iamv1alpha2.UserAuthLimitExceeded {
		return ctrl.Result{Requeue: true, RequeueAfter: r.AuthenticationOptions.AuthenticateRateLimiterDuration}, nil
	}

	return ctrl.Result{}, nil
}

// encryptPassword Encrypt and update the user password
func (r *Reconciler) encryptPassword(ctx context.Context, user *iamv1alpha2.User) error {
	// password is not empty and not encrypted
	if user.Spec.EncryptedPassword != "" && !isEncrypted(user.Spec.EncryptedPassword) {
		password, err := encrypt(user.Spec.EncryptedPassword)
		if err != nil {
			klog.Error(err)
			return err
		}
		user.Spec.EncryptedPassword = password
		if user.Annotations == nil {
			user.Annotations = make(map[string]string)
		}
		user.Annotations[iamv1alpha2.LastPasswordChangeTimeAnnotation] = time.Now().UTC().Format(time.RFC3339)
		// ensure plain text password won't be kept anywhere
		delete(user.Annotations, corev1.LastAppliedConfigAnnotation)
		err = r.Update(ctx, user, &client.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// nolint
func (r *Reconciler) ensureNotControlledByKubefed(ctx context.Context, user *iamv1alpha2.User) error {
	if user.Labels[constants.KubefedManagedLabel] != "false" {
		if user.Labels == nil {
			user.Labels = make(map[string]string, 0)
		}
		user.Labels[constants.KubefedManagedLabel] = "false"
		err := r.Update(ctx, user, &client.UpdateOptions{})
		if err != nil {
			klog.Error(err)
			return err
		}
	}
	return nil
}

func (r *Reconciler) deleteGroupBindings(ctx context.Context, user *iamv1alpha2.User) error {
	// groupBindings that created by kubeshpere will be deleted directly.
	groupBindings := &iamv1alpha2.GroupBinding{}
	return r.Client.DeleteAllOf(ctx, groupBindings, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
}

func (r *Reconciler) deleteRoleBindings(ctx context.Context, user *iamv1alpha2.User) error {
	if len(user.Name) > validation.LabelValueMaxLength {
		// ignore invalid label value error
		return nil
	}

	globalRoleBinding := &iamv1alpha2.GlobalRoleBinding{}
	err := r.Client.DeleteAllOf(ctx, globalRoleBinding, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	workspaceRoleBinding := &iamv1alpha2.WorkspaceRoleBinding{}
	err = r.Client.DeleteAllOf(ctx, workspaceRoleBinding, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	err = r.Client.DeleteAllOf(ctx, clusterRoleBinding, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	roleBindingList := &rbacv1.RoleBindingList{}
	err = r.Client.List(ctx, roleBindingList, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	for _, roleBinding := range roleBindingList.Items {
		err = r.Client.Delete(ctx, &roleBinding)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) deleteLoginRecords(ctx context.Context, user *iamv1alpha2.User) error {
	loginRecord := &iamv1alpha2.LoginRecord{}
	return r.Client.DeleteAllOf(ctx, loginRecord, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
}

// syncUserStatus Update the user status
func (r *Reconciler) syncUserStatus(ctx context.Context, user *iamv1alpha2.User) error {
	// skip status sync if the user is disabled
	if user.Status.State == iamv1alpha2.UserDisabled {
		return nil
	}

	if user.Spec.EncryptedPassword == "" {
		if user.Labels[iamv1alpha2.IdentifyProviderLabel] != "" {
			// mapped user from other identity provider always active until disabled
			if user.Status.State != iamv1alpha2.UserActive {
				user.Status = iamv1alpha2.UserStatus{
					State:              iamv1alpha2.UserActive,
					LastTransitionTime: &metav1.Time{Time: time.Now()},
				}
				err := r.Update(ctx, user, &client.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		} else {
			// empty password is not allowed for normal user
			if user.Status.State != iamv1alpha2.UserDisabled {
				user.Status = iamv1alpha2.UserStatus{
					State:              iamv1alpha2.UserDisabled,
					LastTransitionTime: &metav1.Time{Time: time.Now()},
				}
				err := r.Update(ctx, user, &client.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		}
		// skip auth limit check
		return nil
	}

	// becomes active after password encrypted
	if user.Status.State == "" && isEncrypted(user.Spec.EncryptedPassword) {
		user.Status = iamv1alpha2.UserStatus{
			State:              iamv1alpha2.UserActive,
			LastTransitionTime: &metav1.Time{Time: time.Now()},
		}
		err := r.Update(ctx, user, &client.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	// blocked user, check if need to unblock user
	if user.Status.State == iamv1alpha2.UserAuthLimitExceeded {
		if user.Status.LastTransitionTime != nil &&
			user.Status.LastTransitionTime.Add(r.AuthenticationOptions.AuthenticateRateLimiterDuration).Before(time.Now()) {
			// unblock user
			user.Status = iamv1alpha2.UserStatus{
				State:              iamv1alpha2.UserActive,
				LastTransitionTime: &metav1.Time{Time: time.Now()},
			}
			err := r.Update(ctx, user, &client.UpdateOptions{})
			if err != nil {
				return err
			}
			return nil
		}
	}

	records := &iamv1alpha2.LoginRecordList{}
	// normal user, check user's login records see if we need to block
	err := r.List(ctx, records, client.MatchingLabels{iamv1alpha2.UserReferenceLabel: user.Name})
	if err != nil {
		klog.Error(err)
		return err
	}

	// count failed login attempts during last AuthenticateRateLimiterDuration
	now := time.Now()
	failedLoginAttempts := 0
	for _, loginRecord := range records.Items {
		afterStateTransition := user.Status.LastTransitionTime == nil || loginRecord.CreationTimestamp.After(user.Status.LastTransitionTime.Time)
		if !loginRecord.Spec.Success &&
			afterStateTransition &&
			loginRecord.CreationTimestamp.Add(r.AuthenticationOptions.AuthenticateRateLimiterDuration).After(now) {
			failedLoginAttempts++
		}
	}

	// block user if failed login attempts exceeds maximum tries setting
	if failedLoginAttempts >= r.AuthenticationOptions.AuthenticateRateLimiterMaxTries {
		user.Status = iamv1alpha2.UserStatus{
			State:              iamv1alpha2.UserAuthLimitExceeded,
			Reason:             fmt.Sprintf("Failed login attempts exceed %d in last %s", failedLoginAttempts, r.AuthenticationOptions.AuthenticateRateLimiterDuration),
			LastTransitionTime: &metav1.Time{Time: time.Now()},
		}

		err = r.Update(ctx, user, &client.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func encrypt(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// isEncrypted returns whether the given password is encrypted
func isEncrypted(password string) bool {
	// bcrypt.Cost returns the hashing cost used to create the given hashed
	cost, _ := bcrypt.Cost([]byte(password))
	// cost > 0 means the password has been encrypted
	return cost > 0
}
