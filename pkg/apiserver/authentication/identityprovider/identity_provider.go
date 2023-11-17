package identityprovider

import (
	"context"
	"errors"

	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/server/options"
)

const (
	MappingMethodAuto MappingMethod = "auto"
	// MappingMethodLookup Looks up an existing identity, user identity mapping, and user, but does not automatically
	// provision users or identities. Using this method requires you to manually provision users.
	MappingMethodLookup MappingMethod = "lookup"
	// MappingMethodMixed  A user entity can be mapped with multiple identifyProvider.
	// not supported yet.
	MappingMethodMixed MappingMethod = "mixed"

	SecretLabelIdentityProviderName = "config.kubesphere.io/identityprovider-name"
	SecretTypeIdentityProvider      = "config.kubesphere.io/identityprovider"

	SecretDataKey = "configuration.yaml"

	AnnotationProviderCategory = "config.kubesphere.io/identityprovider-category"
	CategoryGeneric            = "generic"
	CategoryOAuth              = "oauth"
)

var ErrorIdentityProviderNotFound = errors.New("the Identity provider was not found")

type MappingMethod string
type Configuration struct {

	// The provider name.
	Name string `json:"name" yaml:"name"`

	// Defines how new identities are mapped to users when they login. Allowed values are:
	//  - auto:   The default value.The user will automatically create and mapping when login successful.
	//            Fails if a user with that user name is already mapped to another identity.
	//  - lookup: Looks up an existing identity, user identity mapping, and user, but does not automatically
	//            provision users or identities. Using this method requires you to manually provision users.
	//  - mixed:  A user entity can be mapped with multiple identifyProvider.
	MappingMethod MappingMethod `json:"mappingMethod" yaml:"mappingMethod"`

	// DisableLoginConfirmation means that when the user login successfully,
	// reconfirm the account information is not required.
	// Username from IDP must math [a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*
	DisableLoginConfirmation bool `json:"disableLoginConfirmation" yaml:"disableLoginConfirmation"`

	// The type of identify provider
	// OpenIDIdentityProvider LDAPIdentityProvider GitHubIdentityProvider
	Type string `json:"type" yaml:"type"`

	// The options of identify provider
	ProviderOptions options.DynamicOptions `json:"provider" yaml:"provider"`
}

type ConfigurationGetter interface {
	GetConfiguration(ctx context.Context, name string) (*Configuration, error)
}

func NewConfigurationGetter(client client.Client) ConfigurationGetter {
	return &configurationGetter{client}
}

type configurationGetter struct {
	client.Client
}

func (o *configurationGetter) GetConfiguration(ctx context.Context, name string) (*Configuration, error) {
	identityProvider := &Configuration{}
	secrets := &v1.SecretList{}
	err := o.List(ctx, secrets, client.MatchingLabels{SecretLabelIdentityProviderName: name})
	if err != nil {
		return nil, err
	}

	if len(secrets.Items) == 0 {
		return nil, ErrorIdentityProviderNotFound
	}

	// select the first item, because we ensure there will not be a client with the same name
	secret := secrets.Items[0]

	if secret.Type != SecretTypeIdentityProvider {
		return nil, ErrorIdentityProviderNotFound
	}

	err = yaml.Unmarshal(secret.Data[SecretDataKey], identityProvider)
	if err != nil {
		return nil, err
	}

	return identityProvider, nil
}

func UnmarshalTo(secret v1.Secret) (*Configuration, error) {
	config := &Configuration{}
	err := yaml.Unmarshal(secret.Data[SecretDataKey], config)
	if err != nil {
		return nil, err
	}
	return config, nil
}
