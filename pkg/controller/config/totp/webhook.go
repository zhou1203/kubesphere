package totp

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"kubesphere.io/kubesphere/pkg/models/auth"
)

type Webhook struct {
	client.Client
}

func (r *Webhook) ConfigType() corev1.SecretType {
	return auth.SecretTypeTOTPAuthKey
}

func (r *Webhook) Default(_ context.Context, secret *corev1.Secret) error {
	if secret.Type != auth.SecretTypeTOTPAuthKey {
		return nil
	}
	key, err := auth.TOTPAuthKeyFrom(secret)
	if err != nil {
		return fmt.Errorf("failed to get auth key from secret: %v", err)
	}
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[iamv1beta1.UserReferenceLabel] = key.AccountName()
	return nil
}

func (r *Webhook) ValidateCreate(ctx context.Context, secret *corev1.Secret) (warnings admission.Warnings, err error) {
	if secret.Type != auth.SecretTypeTOTPAuthKey {
		return nil, nil
	}
	key, err := auth.TOTPAuthKeyFrom(secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth key from secret: %v", err)
	}

	secrets := &corev1.SecretList{}
	if err := r.List(ctx, secrets, client.MatchingLabels{iamv1beta1.UserReferenceLabel: key.AccountName()}); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %v", err)
	}

	if len(secrets.Items) > 0 {
		exists := secrets.Items[0]
		return nil, fmt.Errorf("user %s is already bind to an other totp auth key: %s", key.AccountName(), exists.Namespace+"/"+exists.Name)
	}
	return nil, nil
}

func (r *Webhook) ValidateUpdate(_ context.Context, oldSecret, newSecret *corev1.Secret) (warnings admission.Warnings, err error) {
	if oldSecret.Type == auth.SecretTypeTOTPAuthKey && newSecret.Type != auth.SecretTypeTOTPAuthKey {
		return nil, fmt.Errorf("the type of secret can not be changed")
	}
	oldKey, err := auth.TOTPAuthKeyFrom(oldSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth key from secret: %v", err)
	}
	newKey, err := auth.TOTPAuthKeyFrom(newSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth key from secret: %v", err)
	}
	if oldKey.AccountName() != newKey.AccountName() {
		return nil, fmt.Errorf("the account name of secret can not be changed")
	}
	return nil, nil
}

func (r *Webhook) ValidateDelete(_ context.Context, _ *corev1.Secret) (warnings admission.Warnings, err error) {
	return nil, nil
}
