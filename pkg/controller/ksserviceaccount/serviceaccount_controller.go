package ksserviceaccount

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	controllerName = "ks-serviceaccount-controller"
	finalizer      = "finalizers.kubesphere.io/serviceaccount"

	messageCreateSecretSuccessfully = "Create token secret successfully"
	reasonInvalidSecret             = "InvalidSecret"
)

type Reconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Logger        logr.Logger
	EventRecorder record.EventRecorder
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.WithValues(req.NamespacedName, "ServiceAccount")
	sa := &corev1alpha1.ServiceAccount{}
	err := r.Get(ctx, req.NamespacedName, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		logger.Error(err, "get serviceaccount failed")
		return ctrl.Result{}, err
	}

	if sa.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(sa, finalizer) {
			deepCopy := sa.DeepCopy()
			deepCopy.Finalizers = append(deepCopy.Finalizers, finalizer)
			if err != nil {
				logger.Error(err, "get secret failed")
				return ctrl.Result{}, err
			}
			if len(sa.Secrets) == 0 {
				secretCreated, err := r.createTokenSecret(ctx, sa)
				if err != nil {
					logger.Error(err, "create secret failed")
					return ctrl.Result{}, err
				}
				logger.V(4).WithName(secretCreated.Name).Info("secret created successfully")

				deepCopy.Secrets = append(deepCopy.Secrets, v1.ObjectReference{
					Namespace: secretCreated.Namespace,
					Name:      secretCreated.Name,
				})
				r.EventRecorder.Event(deepCopy, corev1.EventTypeNormal, constants.SuccessSynced, messageCreateSecretSuccessfully)
			}

			err = r.Update(ctx, deepCopy)
			if err != nil {
				logger.Error(err, "update serviceaccount failed")
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(sa, finalizer) {
			err := r.deleteSecretToken(ctx, sa, logger)
			if err != nil {
				logger.Error(err, "delete secret failed")
				return ctrl.Result{}, err
			}
			_ = controllerutil.RemoveFinalizer(sa, finalizer)
			err = r.Update(ctx, sa)
			if err != nil {
				logger.Error(err, "update serviceaccount failed")
				return ctrl.Result{}, err
			}
		}
	}

	err = r.checkAllSecret(ctx, sa)
	if err != nil {
		logger.Error(err, "failed check secrets")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createTokenSecret(ctx context.Context, sa *corev1alpha1.ServiceAccount) (*v1.Secret, error) {
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", sa.Name),
			Namespace:    sa.Namespace,
			Annotations:  map[string]string{constants.ServiceAccountName: sa.Name},
		},
		Type: constants.SecretTypeKubesphereServiceAccount,
	}

	return secret, r.Client.Create(ctx, secret)
}

func (r *Reconciler) deleteSecretToken(ctx context.Context, sa *corev1alpha1.ServiceAccount, logger logr.Logger) error {
	for _, secretName := range sa.Secrets {
		secret := &v1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: secretName.Namespace, Name: secretName.Name}, secret)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			} else {
				return err
			}
		}
		if err := r.checkSecretToken(secret, sa.Name); err == nil {
			err = r.Delete(ctx, secret)
			if err != nil {
				return err
			}
			logger.V(2).WithName(secretName.Name).Info("delete secret successfully")
		}
	}
	return nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.EventRecorder == nil {
		r.EventRecorder = mgr.GetEventRecorderFor(controllerName)
	}

	if r.Logger.GetSink() == nil {
		r.Logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	}
	return builder.
		ControllerManagedBy(mgr).
		For(
			&corev1alpha1.ServiceAccount{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}

func (r *Reconciler) checkAllSecret(ctx context.Context, sa *corev1alpha1.ServiceAccount) error {
	for _, secretRef := range sa.Secrets {
		secret := &v1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: sa.Namespace, Name: secretRef.Name}, secret)
		if err != nil {
			if errors.IsNotFound(err) {
				r.EventRecorder.Event(sa, corev1.EventTypeWarning, reasonInvalidSecret, err.Error())
				continue
			}
			return err
		}
		if err := r.checkSecretToken(secret, sa.Name); err != nil {
			r.EventRecorder.Event(sa, corev1.EventTypeWarning, reasonInvalidSecret, err.Error())
		}
	}
	return nil
}

// checkSecretTokens Check if there has valid token, and the invalid token reference will be deleted
func (r *Reconciler) checkSecretToken(secret *v1.Secret, subjectName string) error {
	if secret.Type != constants.SecretTypeKubesphereServiceAccount {
		return fmt.Errorf("unsupported secret %s type: %s", secret.Name, secret.Type)
	}

	saName := secret.Annotations[constants.ServiceAccountName]
	if saName != subjectName {
		return fmt.Errorf("incorrect subject name %s", saName)
	}
	return nil
}
