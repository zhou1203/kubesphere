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

package oauth

import (
	"time"

	"kubesphere.io/kubesphere/pkg/server/options"
)

type GrantHandlerType string
type MappingMethod string
type IdentityProviderType string

const (
	MappingMethodAuto MappingMethod = "auto"
	// MappingMethodLookup Looks up an existing identity, user identity mapping, and user, but does not automatically
	// provision users or identities. Using this method requires you to manually provision users.
	MappingMethodLookup MappingMethod = "lookup"
	// MappingMethodMixed  A user entity can be mapped with multiple identifyProvider.
	// not supported yet.
	MappingMethodMixed MappingMethod = "mixed"

	DefaultIssuer string = "https://ks-console.kubesphere-system.svc"
)

type Options struct {
	// An Issuer Identifier is a case-sensitive URL using the https scheme that contains scheme,
	// host, and optionally, port number and path components and no query or fragment components.
	Issuer string `json:"issuer,omitempty" yaml:"issuer,omitempty"`

	// RSA private key file used to sign the id token
	SignKey string `json:"signKey,omitempty" yaml:"signKey,omitempty"`

	// Raw RSA private key. Base64 encoded PEM file
	SignKeyData string `json:"-,omitempty" yaml:"signKeyData,omitempty"`

	// AccessTokenMaxAgeSeconds  control the lifetime of access tokens. The default lifetime is 24 hours.
	// 0 means no expiration.
	AccessTokenMaxAge time.Duration `json:"accessTokenMaxAge" yaml:"accessTokenMaxAge"`

	// Inactivity timeout for tokens
	// The value represents the maximum amount of time that can occur between
	// consecutive uses of the token. Tokens become invalid if they are not
	// used within this temporal window. The user will need to acquire a new
	// token to regain access once a token times out.
	// This value needs to be set only if the default set in configuration is
	// not appropriate for this client. Valid values are:
	// - 0: Tokens for this client never time out
	// - X: Tokens time out if there is no activity
	// The current minimum allowed value for X is 5 minutes
	AccessTokenInactivityTimeout time.Duration `json:"accessTokenInactivityTimeout" yaml:"accessTokenInactivityTimeout"`
}

type IdentityProviderOptions struct {
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
	Provider options.DynamicOptions `json:"provider" yaml:"provider"`
}

type Token struct {
	// AccessToken is the token that authorizes and authenticates
	// the requests.
	AccessToken string `json:"access_token"`

	// TokenType is the type of token.
	// The Type method returns either this or "Bearer", the default.
	TokenType string `json:"token_type,omitempty"`

	// RefreshToken is a token that's used by the application
	// (as opposed to the user) to refresh the access token
	// if it expires.
	RefreshToken string `json:"refresh_token,omitempty"`

	// ID Token value associated with the authenticated session.
	IDToken string `json:"id_token,omitempty"`

	// ExpiresIn is the optional expiration second of the access token.
	ExpiresIn int `json:"expires_in,omitempty"`
}

func NewOptions() *Options {
	return &Options{
		Issuer:                       DefaultIssuer,
		AccessTokenMaxAge:            time.Hour * 2,
		AccessTokenInactivityTimeout: time.Hour * 2,
	}
}
