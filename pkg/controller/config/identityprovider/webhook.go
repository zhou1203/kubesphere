package identityprovider

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
)

var once sync.Once

type WebhookHandler struct {
	client.Client
	getter identityprovider.ConfigurationGetter
}

func (w *WebhookHandler) Default(ctx context.Context, secret *corev1.Secret) error {
	configuration, err := identityprovider.UnmarshalTo(*secret)
	if err != nil {
		return err
	}

	if configuration.Name != "" {
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels[identityprovider.SecretLabelIdentityProviderName] = configuration.Name
	}

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	factory := identityprovider.GetGenericProviderFactory(configuration.Type)
	if factory != nil {
		secret.Annotations[identityprovider.AnnotationProviderCategory] = identityprovider.CategoryGeneric
	} else if factory := identityprovider.GetOAuthProviderFactory(configuration.Type); factory != nil {
		secret.Annotations[identityprovider.AnnotationProviderCategory] = identityprovider.CategoryOAuth
	}

	return nil
}

func (w *WebhookHandler) ValidateCreate(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	idp, err := identityprovider.UnmarshalTo(*secret)
	if err != nil {
		return nil, err
	}
	if idp.Name == "" {
		return nil, errors.New("invalid Identity Provider, please ensure that the provider name is not empty")
	}

	exist, err := w.exist(ctx, idp.Name)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, fmt.Errorf("invalid provider, provider name '%s' already exists", idp.Name)
	}

	return nil, nil
}

func (w *WebhookHandler) ValidateUpdate(ctx context.Context, old, new *corev1.Secret) (admission.Warnings, error) {
	oldIdp, err := identityprovider.UnmarshalTo(*old)
	if err != nil {
		return nil, err
	}

	newIdp, err := identityprovider.UnmarshalTo(*new)
	if err != nil {
		return nil, err
	}

	if newIdp.Name != oldIdp.Name {
		return nil, fmt.Errorf("cannot change provider name")
	}
	return nil, nil
}

func (w *WebhookHandler) ValidateDelete(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	return nil, nil
}

func (w *WebhookHandler) ConfigType() corev1.SecretType {
	return identityprovider.SecretTypeIdentityProvider
}

func (w *WebhookHandler) exist(ctx context.Context, clientName string) (bool, error) {
	once.Do(func() {
		w.getter = identityprovider.NewConfigurationGetter(w.Client)
	})

	authClient, err := w.getter.GetConfiguration(ctx, clientName)
	if err != nil {
		return false, err
	}
	if authClient == nil {
		return false, nil
	}
	return true, nil
}
