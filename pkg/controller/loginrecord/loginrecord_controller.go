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

package loginrecord

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"
)

const controllerName = "loginrecord-controller"

type Reconciler struct {
	client.Client

	recorder record.EventRecorder

	loginHistoryRetentionPeriod time.Duration
	loginHistoryMaximumEntries  int
}

func NewReconciler(loginHistoryRetentionPeriod time.Duration, loginHistoryMaximumEntries int) *Reconciler {
	return &Reconciler{
		loginHistoryRetentionPeriod: loginHistoryRetentionPeriod,
		loginHistoryMaximumEntries:  loginHistoryMaximumEntries,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	loginRecord := &iamv1beta1.LoginRecord{}
	if err := r.Get(ctx, req.NamespacedName, loginRecord); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)

	}

	if !loginRecord.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is being deleted
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	user, err := r.userForLoginRecord(ctx, loginRecord)
	if err != nil {
		// delete orphan object
		if errors.IsNotFound(err) {
			if err = r.Delete(ctx, loginRecord); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err = r.updateUserLastLoginTime(ctx, user, loginRecord); err != nil {
		return ctrl.Result{}, err
	}

	if err = r.shrinkEntriesFor(ctx, user); err != nil {
		return ctrl.Result{}, err
	}

	result := ctrl.Result{}
	now := time.Now()
	// login record beyonds retention period
	if loginRecord.CreationTimestamp.Add(r.loginHistoryRetentionPeriod).Before(now) {
		if err = r.Delete(ctx, loginRecord, client.GracePeriodSeconds(0)); err != nil {
			return ctrl.Result{}, err
		}
	} else { // put item back into the queue
		result = ctrl.Result{
			RequeueAfter: loginRecord.CreationTimestamp.Add(r.loginHistoryRetentionPeriod).Sub(now),
		}
	}
	r.recorder.Event(loginRecord, corev1.EventTypeNormal, kscontroller.Synced, kscontroller.MessageResourceSynced)
	return result, nil
}

// updateUserLastLoginTime accepts a login object and set user lastLoginTime field
func (r *Reconciler) updateUserLastLoginTime(ctx context.Context, user *iamv1beta1.User, loginRecord *iamv1beta1.LoginRecord) error {
	// update lastLoginTime
	if user.DeletionTimestamp.IsZero() &&
		(user.Status.LastLoginTime == nil || user.Status.LastLoginTime.Before(&loginRecord.CreationTimestamp)) {
		user.Status.LastLoginTime = &loginRecord.CreationTimestamp

		return r.Update(ctx, user)
	}
	return nil
}

// shrinkEntriesFor will delete old entries out of limit
func (r *Reconciler) shrinkEntriesFor(ctx context.Context, user *iamv1beta1.User) error {
	loginRecords := &iamv1beta1.LoginRecordList{}
	if err := r.List(ctx, loginRecords, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{iamv1beta1.UserReferenceLabel: user.Name})}); err != nil {
		return err
	}

	if len(loginRecords.Items) <= r.loginHistoryMaximumEntries {
		return nil
	}

	sort.Slice(loginRecords.Items, func(i, j int) bool {
		return loginRecords.Items[j].CreationTimestamp.After(loginRecords.Items[i].CreationTimestamp.Time)
	})
	oldEntries := loginRecords.Items[:len(loginRecords.Items)-r.loginHistoryMaximumEntries]
	for i := range oldEntries {
		if err := r.Delete(ctx, &oldEntries[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) userForLoginRecord(ctx context.Context, loginRecord *iamv1beta1.LoginRecord) (*iamv1beta1.User, error) {
	username, ok := loginRecord.Labels[iamv1beta1.UserReferenceLabel]
	if !ok || len(username) == 0 {
		klog.V(4).Info("login doesn't belong to any user")
		return nil, errors.NewNotFound(iamv1beta1.Resource(iamv1beta1.ResourcesSingularUser), username)
	}
	user := &iamv1beta1.User{}
	if err := r.Get(ctx, client.ObjectKey{Name: username}, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.Client = mgr.GetClient()

	return builder.
		ControllerManagedBy(mgr).
		For(
			&iamv1beta1.LoginRecord{},
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
