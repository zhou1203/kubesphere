package oauthclient

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
	"kubesphere.io/kubesphere/pkg/models/auth"
)

type WebhookHandler struct {
	client.Client
}

func (v *WebhookHandler) Default(_ context.Context, secret *v1.Secret) error {
	oc := &auth.OAuthClient{}
	err := yaml.Unmarshal(secret.Data[auth.SecretDataKey], oc)
	if err != nil {
		return err
	}

	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[auth.SecretLabelClientName] = oc.Name

	if oc.GrantMethod == auth.GrantMethodNone {
		oc.GrantMethod = auth.GrantMethodAuto
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
	secret.Data[auth.SecretDataKey] = marshal

	return nil
}

func (v *WebhookHandler) ValidateCreate(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	oc := &auth.OAuthClient{}
	err := yaml.Unmarshal(secret.Data[auth.SecretDataKey], oc)
	if err != nil {
		return nil, err
	}
	return v.validate(ctx, oc)
}

func (v *WebhookHandler) ValidateUpdate(ctx context.Context, old, new *corev1.Secret) (admission.Warnings, error) {
	newOc := &auth.OAuthClient{}
	err := yaml.Unmarshal(new.Data[auth.SecretDataKey], newOc)
	if err != nil {
		return nil, err
	}
	oldOc := &auth.OAuthClient{}
	err = yaml.Unmarshal(old.Data[auth.SecretDataKey], newOc)
	if err != nil {
		return nil, err
	}

	// the client
	if newOc.Name != oldOc.Name {
		return nil, fmt.Errorf("cannot change client name")
	}

	return v.validate(ctx, newOc)
}

func (v *WebhookHandler) ValidateDelete(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	return nil, nil
}

func (v *WebhookHandler) ConfigType() corev1.SecretType {
	return auth.SecretTypeOAuthClient
}

func (v *WebhookHandler) validate(ctx context.Context, oc *auth.OAuthClient) (admission.Warnings, error) {
	if oc.Name == "" {
		return nil, fmt.Errorf("invalid OAuth client, please ensure that the client name is not empty")
	}

	// check if the client name is unique
	secretList := &v1.SecretList{}
	err := v.Client.List(ctx, secretList, client.MatchingLabels{auth.SecretLabelClientName: oc.Name})
	if err != nil {
		return nil, err
	}
	if len(secretList.Items) != 0 {
		return nil, fmt.Errorf("invalid OAuth client, client name %s already exists", oc.Name)
	}

	err = auth.ValidateClient(*oc)
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

func generatePassword(length int) string {
	characters := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	password := make([]byte, length)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range password {
		password[i] = characters[r.Intn(len(characters))]
	}

	return string(password)
}
