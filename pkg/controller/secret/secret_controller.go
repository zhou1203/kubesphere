package secret

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	controllerName = "secrets-controller"
)

type Reconciler struct {
	client.Client
	Logger        logr.Logger
	EventRecorder record.EventRecorder
	TokenIssuer   token.Issuer
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.WithValues(req.NamespacedName, "Secret")
	secret := &v1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		return ctrl.Result{}, err
	}

	saName := secret.Annotations[constants.ServiceAccountName]

	if secret.Type == constants.SecretTypeKubesphereServiceAccount && secret.Data[constants.SecretTokenKey] == nil &&
		saName != "" {
		sa := &corev1alpha1.ServiceAccount{}
		err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: saName}, sa)
		if err != nil {
			if errors.IsNotFound(err) {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			logger.Error(err, "get serviceaccount failed")
			return ctrl.Result{}, err
		}

		tokenTo, err := r.issueTokenTo(sa)
		if err != nil {
			logger.Error(err, "issue token failed")
			return ctrl.Result{}, err
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte, 0)
		}
		secret.Data[constants.SecretTokenKey] = []byte(tokenTo.AccessToken)
		err = r.Update(ctx, secret)
		if err != nil {
			logger.Error(err, "update secret failed")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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
			&v1.Secret{},
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}

func (r *Reconciler) issueTokenTo(sa *corev1alpha1.ServiceAccount) (*oauth.Token, error) {
	name := fmt.Sprintf(constants.ServiceAccountTokenSubFormat, sa.Namespace, sa.Name)
	accessToken, err := r.TokenIssuer.IssueTo(&token.IssueRequest{
		User: &user.DefaultInfo{
			Name:   name,
			Groups: nil, // TODO add group
			Extra:  nil,
		},
		Claims: token.Claims{TokenType: token.AccessToken},
	})
	if err != nil {
		return nil, err
	}

	result := oauth.Token{
		AccessToken: accessToken,
		// The OAuth 2.0 token_type response parameter value MUST be Bearer,
		// as specified in OAuth 2.0 Bearer Token Usage [RFC6750]
		TokenType: "Bearer",
	}
	return &result, nil
}
