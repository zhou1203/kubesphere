package config

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/controller/config/identityprovider"
	"kubesphere.io/kubesphere/pkg/controller/config/oauthclient"
)

type configWebhook struct {
	client.Client
	WebhookFactory *WebhookFactory
}

func (c *configWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	secret := obj.(*v1.Secret)
	validator := c.WebhookFactory.GetValidator(secret.Type)
	if validator != nil {
		return validator.ValidateCreate(ctx, secret)
	}
	return nil, nil
}

func (c *configWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (warnings admission.Warnings, err error) {
	newSecret := newObj.(*v1.Secret)
	oldSecret := oldObj.(*v1.Secret)
	if validator := c.WebhookFactory.GetValidator(newSecret.Type); validator != nil {
		return validator.ValidateUpdate(ctx, oldSecret, newSecret)
	}
	return nil, nil
}

func (c *configWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (warnings admission.Warnings, err error) {
	secret := obj.(*v1.Secret)
	validator := c.WebhookFactory.GetValidator(secret.Type)
	if validator != nil {
		return validator.ValidateDelete(ctx, secret)
	}
	return nil, nil
}

func (c *configWebhook) Default(ctx context.Context, obj runtime.Object) error {
	secret := obj.(*v1.Secret)
	if secret.Namespace != constants.KubeSphereNamespace {
		return nil
	}

	defaulter := c.WebhookFactory.GetDefaulter(secret.Type)
	if defaulter != nil {
		return defaulter.Default(ctx, secret)
	}

	return nil
}

var _ admission.CustomDefaulter = &configWebhook{}
var _ admission.CustomValidator = &configWebhook{}

// SetupWebhookWithManager TODO make the webhook url be customize
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	factory := NewWebhookFactory()
	oauthWebhookHandler := &oauthclient.WebhookHandler{Client: mgr.GetClient()}
	factory.RegisterValidator(oauthWebhookHandler)
	factory.RegisterDefaulter(oauthWebhookHandler)
	identityProviderWebhookHandler := &identityprovider.WebhookHandler{Client: mgr.GetClient()}
	factory.RegisterValidator(identityProviderWebhookHandler)
	factory.RegisterDefaulter(identityProviderWebhookHandler)

	webhook := &configWebhook{
		Client:         mgr.GetClient(),
		WebhookFactory: factory,
	}

	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(webhook).
		WithDefaulter(webhook).
		For(&v1.Secret{}).
		Complete()
}
