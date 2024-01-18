package auth

import (
	"context"
	"fmt"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/config"
	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	TOTPAuthKeySecretNameFormat = "totp-auth-key-%s"
	TOTPAuthKeyRefAnnotation    = "iam.kubesphere.io/totp-auth-key-ref"
	ConfigTypeTOTPAuthKey       = "totp-auth-key"
	SecretTypeTOTPAuthKey       = "iam.kubesphere.io/" + ConfigTypeTOTPAuthKey
	TOTPAuthKey                 = "authKey"
)

type TOTPAuthenticator interface {
	Authenticate(ctx context.Context, username string, passcode string) (authuser.Info, error)
}

type TOTPOperator interface {
	GenerateAuthKey(ctx context.Context, username string) (string, error)
	Bind(ctx context.Context, username string, authKey string, otp string) error
	Unbind(ctx context.Context, username string, passcode string) error
}

type totpAuthenticator struct {
	reader client.Reader
}

type totpOperator struct {
	authOptions *authentication.Options
	client      client.Client
}

func (t *totpOperator) GenerateAuthKey(ctx context.Context, username string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      t.authOptions.OAuthOptions.Issuer,
		AccountName: username,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", err
	}
	return key.String(), nil
}

func (t *totpOperator) Bind(ctx context.Context, username string, authKey string, passcode string) error {
	user := &iamv1beta1.User{}
	if err := t.client.Get(ctx, types.NamespacedName{Name: username}, user); err != nil {
		return fmt.Errorf("failed to get user %s: %s", username, err)
	}
	if user.Annotations[TOTPAuthKeyRefAnnotation] != "" {
		return fmt.Errorf("user %s is already bind to an other totp auth key", username)
	}
	key, err := otp.NewKeyFromURL(authKey)
	if err != nil {
		return fmt.Errorf("invalid auth key: %s", err)
	}
	if valid := totp.Validate(passcode, key.Secret()); !valid {
		return fmt.Errorf("invalid passcode")
	}
	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name: fmt.Sprintf(TOTPAuthKeySecretNameFormat, username),
			Labels: map[string]string{
				iamv1beta1.UserReferenceLabel: username,
				config.GenericConfigTypeLabel: ConfigTypeTOTPAuthKey,
			},
			Namespace: constants.KubeSphereNamespace,
		},
		Type: SecretTypeTOTPAuthKey,
		StringData: map[string]string{
			TOTPAuthKey: key.String(),
		},
	}
	if err := t.client.Create(ctx, secret); err != nil {
		return fmt.Errorf("failed to bind auth key: %s", err)
	}
	return nil
}

func (t *totpOperator) Unbind(ctx context.Context, username string, passcode string) error {
	user := &iamv1beta1.User{}
	if err := t.client.Get(ctx, types.NamespacedName{Name: username}, user); err != nil {
		return fmt.Errorf("failed to get user %s: %s", username, err)
	}
	authKeyRef := user.Annotations[TOTPAuthKeyRefAnnotation]
	if authKeyRef == "" {
		return nil
	}
	secret := &corev1.Secret{}
	if err := t.client.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: authKeyRef}, secret); err != nil {
		if errors.IsNotFound(err) {
			delete(user.Annotations, TOTPAuthKeyRefAnnotation)
			return t.client.Update(ctx, user)
		}
		return fmt.Errorf("failed to get totp auth key %s: %s", authKeyRef, err)
	}
	key, err := TOTPAuthKeyFrom(secret)
	if err != nil {
		klog.Warningf("failed to parse totp auth key %s", authKeyRef)
		delete(user.Annotations, TOTPAuthKeyRefAnnotation)
		return t.client.Update(ctx, user)
	}
	if key.AccountName() != username {
		klog.Warningf("totp auth key not match: %s, %s", username, authKeyRef)
		delete(user.Annotations, TOTPAuthKeyRefAnnotation)
		return t.client.Update(ctx, user)
	}
	if valid := totp.Validate(passcode, key.Secret()); !valid {
		return fmt.Errorf("invalid passcode")
	}
	if err := t.client.Delete(ctx, secret); err != nil {
		return fmt.Errorf("failed to delete totp auth key %s: %s", authKeyRef, err)
	}
	return nil
}

func TOTPAuthKeyFrom(secret *corev1.Secret) (*otp.Key, error) {
	if secret.Type != SecretTypeTOTPAuthKey {
		return nil, fmt.Errorf("invald secret type")
	}
	return otp.NewKeyFromURL(string(secret.Data[TOTPAuthKey]))
}

func (o *totpAuthenticator) Authenticate(ctx context.Context, username string, passcode string) (authuser.Info, error) {
	user := &iamv1beta1.User{}
	if err := o.reader.Get(ctx, client.ObjectKey{Name: username}, user); err != nil {
		return nil, err
	}

	authKeyRef := user.Annotations[TOTPAuthKeyRefAnnotation]
	if authKeyRef == "" {
		klog.Warningf("user %s does not have otp auth key ref", username)
		return nil, fmt.Errorf("invalid passcode")
	}

	secret := &corev1.Secret{}
	if err := o.reader.Get(ctx, client.ObjectKey{Namespace: constants.KubeSphereNamespace, Name: authKeyRef}, secret); err != nil {
		klog.Warningf("failed to get secret %s: %s", authKeyRef, err)
		return nil, fmt.Errorf("invalid passcode")
	}

	authKey := string(secret.Data[TOTPAuthKey])
	key, err := otp.NewKeyFromURL(authKey)
	if err != nil {
		klog.Warningf("failed to parse otp auth key %s", authKey)
		return nil, fmt.Errorf("invalid passcode")
	}

	if valid := totp.Validate(passcode, key.Secret()); !valid {
		return nil, fmt.Errorf("invalid passcode")
	}

	return &authuser.DefaultInfo{Name: user.Name}, nil
}

func NewTOTPAuthenticator(reader client.Reader) TOTPAuthenticator {
	return &totpAuthenticator{
		reader: reader,
	}
}

func NewTOTPOperator(client client.Client, authOption *authentication.Options) TOTPOperator {
	return &totpOperator{authOptions: authOption, client: client}
}
