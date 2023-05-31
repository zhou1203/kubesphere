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
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	controllerName = "user-controller"
	// user finalizer
	finalizer       = "finalizers.kubesphere.io/users"
	interval        = time.Second
	timeout         = 15 * time.Second
	syncFailMessage = "Failed to sync: %s"
)

// Reconciler reconciles a User object
type Reconciler struct {
	client.Client
	KubeconfigClient      kubeconfig.Interface
	AuthenticationOptions *authentication.Options
	Logger                logr.Logger
	Scheme                *runtime.Scheme
	Recorder              record.EventRecorder
	ClusterClientSet      clusterclient.Interface
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
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		For(&iamv1beta1.User{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.Logger.WithValues("user", req.NamespacedName)
	user := &iamv1beta1.User{}
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
				r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			if err = r.deleteGroupBindings(ctx, user); err != nil {
				r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			if err = r.deleteLoginRecords(ctx, user); err != nil {
				r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			user.Finalizers = sliceutil.RemoveString(user.ObjectMeta.Finalizers, func(item string) bool {
				return item == finalizer
			})

			if err = r.Update(ctx, user, &client.UpdateOptions{}); err != nil {
				klog.Error(err)
				r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
				return ctrl.Result{}, err
			}
		}

		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, err
	}

	if r.ClusterClientSet != nil {
		if err := r.sync(ctx, user); err != nil {
			return ctrl.Result{}, err
		}
		if err = r.encryptPassword(ctx, user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
		if err = r.syncUserStatus(ctx, user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
	}

	if r.KubeconfigClient != nil {
		// ensure user KubeconfigClient configmap is created
		if err = r.KubeconfigClient.CreateKubeConfig(user); err != nil {
			klog.Error(err)
			r.Recorder.Event(user, corev1.EventTypeWarning, constants.FailedSynced, fmt.Sprintf(syncFailMessage, err))
			return ctrl.Result{}, err
		}
	}

	r.Recorder.Event(user, corev1.EventTypeNormal, constants.SuccessSynced, constants.MessageResourceSynced)

	// block user for AuthenticateRateLimiterDuration duration, after that put it back to the queue to unblock
	if user.Status.State == iamv1beta1.UserAuthLimitExceeded {
		return ctrl.Result{Requeue: true, RequeueAfter: r.AuthenticationOptions.AuthenticateRateLimiterDuration}, nil
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) sync(ctx context.Context, user *iamv1beta1.User) error {
	clusters, err := r.ClusterClientSet.ListCluster(ctx)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if err := r.syncUser(ctx, cluster, user); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) syncUser(ctx context.Context, cluster clusterv1alpha1.Cluster, template *iamv1beta1.User) error {
	if r.ClusterClientSet.IsHostCluster(&cluster) {
		return nil
	}
	clusterClient, err := r.ClusterClientSet.GetClusterClient(cluster.Name)
	if err != nil {
		return err
	}
	user := &iamv1beta1.User{}
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: template.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			user = &iamv1beta1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:        template.Name,
					Labels:      template.Labels,
					Annotations: template.Annotations,
				},
				Spec:   template.Spec,
				Status: template.Status,
			}
			return clusterClient.Create(ctx, user)
		}
		return err
	}
	if !reflect.DeepEqual(user.Spec, template.Spec) ||
		!reflect.DeepEqual(user.Status, template.Status) ||
		!reflect.DeepEqual(user.Labels, template.Labels) ||
		!reflect.DeepEqual(user.Annotations, template.Annotations) {
		user.Labels = template.Labels
		user.Annotations = template.Annotations
		user.Spec = template.Spec
		user.Status = template.Status
		return clusterClient.Update(ctx, user)
	}
	return err
}

// encryptPassword Encrypt and update the user password
func (r *Reconciler) encryptPassword(ctx context.Context, user *iamv1beta1.User) error {
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
		user.Annotations[iamv1beta1.LastPasswordChangeTimeAnnotation] = time.Now().UTC().Format(time.RFC3339)
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
func (r *Reconciler) ensureNotControlledByKubefed(ctx context.Context, user *iamv1beta1.User) error {
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

func (r *Reconciler) deleteGroupBindings(ctx context.Context, user *iamv1beta1.User) error {
	// groupBindings that created by kubeshpere will be deleted directly.
	groupBindings := &iamv1beta1.GroupBinding{}
	return r.Client.DeleteAllOf(ctx, groupBindings, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
}

func (r *Reconciler) deleteRoleBindings(ctx context.Context, user *iamv1beta1.User) error {
	if len(user.Name) > validation.LabelValueMaxLength {
		// ignore invalid label value error
		return nil
	}

	globalRoleBinding := &iamv1beta1.GlobalRoleBinding{}
	err := r.Client.DeleteAllOf(ctx, globalRoleBinding, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	workspaceRoleBinding := &iamv1beta1.WorkspaceRoleBinding{}
	err = r.Client.DeleteAllOf(ctx, workspaceRoleBinding, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	err = r.Client.DeleteAllOf(ctx, clusterRoleBinding, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
	if err != nil {
		return err
	}

	roleBindingList := &rbacv1.RoleBindingList{}
	err = r.Client.List(ctx, roleBindingList, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
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

func (r *Reconciler) deleteLoginRecords(ctx context.Context, user *iamv1beta1.User) error {
	loginRecord := &iamv1beta1.LoginRecord{}
	return r.Client.DeleteAllOf(ctx, loginRecord, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
}

// syncUserStatus Update the user status
func (r *Reconciler) syncUserStatus(ctx context.Context, user *iamv1beta1.User) error {
	// skip status sync if the user is disabled
	if user.Status.State == iamv1beta1.UserDisabled {
		return nil
	}
	if user.Spec.EncryptedPassword == "" {
		if user.Labels[iamv1beta1.IdentifyProviderLabel] != "" {
			// mapped user from other identity provider always active until disabled
			if user.Status.State != iamv1beta1.UserActive {
				user.Status = iamv1beta1.UserStatus{
					State:              iamv1beta1.UserActive,
					LastTransitionTime: &metav1.Time{Time: time.Now()},
				}
				err := r.Update(ctx, user, &client.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		} else {
			// empty password is not allowed for normal user
			if user.Status.State != iamv1beta1.UserDisabled {
				user.Status = iamv1beta1.UserStatus{
					State:              iamv1beta1.UserDisabled,
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
		user.Status = iamv1beta1.UserStatus{
			State:              iamv1beta1.UserActive,
			LastTransitionTime: &metav1.Time{Time: time.Now()},
		}
		err := r.Update(ctx, user, &client.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	// blocked user, check if need to unblock user
	if user.Status.State == iamv1beta1.UserAuthLimitExceeded {
		if user.Status.LastTransitionTime != nil &&
			user.Status.LastTransitionTime.Add(r.AuthenticationOptions.AuthenticateRateLimiterDuration).Before(time.Now()) {
			// unblock user
			user.Status = iamv1beta1.UserStatus{
				State:              iamv1beta1.UserActive,
				LastTransitionTime: &metav1.Time{Time: time.Now()},
			}
			err := r.Update(ctx, user, &client.UpdateOptions{})
			if err != nil {
				return err
			}
			return nil
		}
	}

	records := &iamv1beta1.LoginRecordList{}
	// normal user, check user's login records see if we need to block
	err := r.List(ctx, records, client.MatchingLabels{iamv1beta1.UserReferenceLabel: user.Name})
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
		user.Status = iamv1beta1.UserStatus{
			State:              iamv1beta1.UserAuthLimitExceeded,
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
