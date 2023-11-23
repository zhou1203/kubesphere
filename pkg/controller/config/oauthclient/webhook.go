package oauthclient

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
)

var once sync.Once

type WebhookHandler struct {
	client.Client
	getter oauth.OAuthClientGetter
}

func (v *WebhookHandler) Default(_ context.Context, secret *v1.Secret) error {
	oc, err := oauth.UnmarshalTo(*secret)
	if err != nil {
		return err
	}

	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[oauth.SecretLabelClientName] = oc.Name

	if oc.GrantMethod == oauth.GrantMethodNone {
		oc.GrantMethod = oauth.GrantMethodAuto
	}
	if oc.Secret == "" {
		oc.Secret = generatePassword(32)
	}
	if oc.AccessTokenMaxAge == 0 {
		oc.AccessTokenMaxAge = 7200
	}
	if oc.AccessTokenInactivityTimeout == 0 {
		oc.AccessTokenInactivityTimeout = 7200
	}
	marshal, err := yaml.Marshal(oc)
	if err != nil {
		return err
	}
	secret.Data[oauth.SecretDataKey] = marshal

	return nil
}

func (v *WebhookHandler) ValidateCreate(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	oc, err := oauth.UnmarshalTo(*secret)
	if err != nil {
		return nil, err
	}
	if oc.Name != "" {
		exist, err := v.clientExist(ctx, oc.Name)
		if err != nil {
			return nil, err
		}
		if exist {
			return nil, fmt.Errorf("invalid OAuth client, client name '%s' already exists", oc.Name)
		}
	}

	return validate(oc)
}

func (v *WebhookHandler) ValidateUpdate(ctx context.Context, old, new *corev1.Secret) (admission.Warnings, error) {
	newOc, err := oauth.UnmarshalTo(*new)
	if err != nil {
		return nil, err
	}
	oldOc, err := oauth.UnmarshalTo(*old)
	if err != nil {
		return nil, err
	}

	if newOc.Name != oldOc.Name {
		return nil, fmt.Errorf("cannot change client name")
	}

	return validate(newOc)
}

func (v *WebhookHandler) ValidateDelete(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	return nil, nil
}

func (v *WebhookHandler) ConfigType() corev1.SecretType {
	return oauth.SecretTypeOAuthClient
}

func validate(oc *oauth.OAuthClient) (admission.Warnings, error) {
	if oc.Name == "" {
		return nil, fmt.Errorf("invalid OAuth client, please ensure that the client name is not empty")
	}
	err := oauth.ValidateClient(*oc)
	if err != nil {
		return nil, err
	}

	//Other scope values MAY be present.
	//Scope values used that are not understood by an implementation SHOULD be ignored.
	if !oauth.IsValidScopes(oc.ScopeRestrictions) {
		warnings := fmt.Sprintf("some requested scopes were invalid: %v", oc.ScopeRestrictions)
		return []string{warnings}, nil
	}
	return nil, nil
}

func (v *WebhookHandler) clientExist(ctx context.Context, clientName string) (bool, error) {
	once.Do(func() {
		v.getter = oauth.NewOAuthClientGetter(v.Client)
	})
	_, err := v.getter.GetOAuthClient(ctx, clientName)
	if err != nil {
		if err == oauth.ErrorClientNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func generatePassword(length int) string {
	characters := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	password := make([]byte, length)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range password {
		password[i] = characters[r.Intn(len(characters))]
	}

	return string(password)
}
