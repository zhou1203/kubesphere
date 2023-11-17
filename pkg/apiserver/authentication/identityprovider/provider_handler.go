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

package identityprovider

import (
	"errors"
	"sync"
)

var (
	oauthProviderFactories   = make(map[string]OAuthProviderFactory)
	genericProviderFactories = make(map[string]GenericProviderFactory)

	ProviderFactoryNotExist = errors.New("provider factory not exist")
)

type Handler interface {
	GetGenericProvider(providerName string) (GenericProvider, bool)
	GetOAuthProvider(providerName string) (OAuthProvider, bool)
	RegisterGenericProvider(provider *Configuration) error
	RegisterOAuthProvider(provider *Configuration) error
	DeleteOAuthProvider(name string)
	DeleteGenericProvider(name string)
}

func NewHandler() Handler {
	return &threadSafeHandler{
		oauthProviders:   make(map[string]OAuthProvider),
		genericProviders: make(map[string]GenericProvider),
		oauthMutex:       sync.RWMutex{},
		genericMutex:     sync.RWMutex{},
	}
}

type threadSafeHandler struct {
	oauthProviders   map[string]OAuthProvider
	genericProviders map[string]GenericProvider
	oauthMutex       sync.RWMutex
	genericMutex     sync.RWMutex
}

func (i *threadSafeHandler) DeleteOAuthProvider(name string) {
	i.oauthMutex.Lock()
	defer i.oauthMutex.Unlock()
	delete(i.oauthProviders, name)
}

func (i *threadSafeHandler) DeleteGenericProvider(name string) {
	i.genericMutex.Lock()
	defer i.genericMutex.Unlock()
	delete(i.genericProviders, name)
}

func (i *threadSafeHandler) GetGenericProvider(providerName string) (GenericProvider, bool) {
	i.genericMutex.RLock()
	defer i.genericMutex.RUnlock()
	provider, exist := i.genericProviders[providerName]
	return provider, exist
}

func (i *threadSafeHandler) GetOAuthProvider(providerName string) (OAuthProvider, bool) {
	i.oauthMutex.RLock()
	defer i.oauthMutex.RUnlock()
	provider, exist := i.oauthProviders[providerName]
	return provider, exist
}

func (i *threadSafeHandler) RegisterGenericProvider(identityProvider *Configuration) error {
	factory, exist := genericProviderFactories[identityProvider.Type]
	if !exist {
		return ProviderFactoryNotExist
	}
	provider, err := factory.Create(identityProvider.ProviderOptions)
	if err != nil {
		return err
	}
	i.genericMutex.Lock()
	defer i.genericMutex.Unlock()
	i.genericProviders[identityProvider.Name] = provider
	return nil
}

func (i *threadSafeHandler) RegisterOAuthProvider(identityProvider *Configuration) error {
	factory, exist := oauthProviderFactories[identityProvider.Type]
	if !exist {
		return ProviderFactoryNotExist
	}
	provider, err := factory.Create(identityProvider.ProviderOptions)
	if err != nil {
		return err
	}
	i.oauthMutex.Lock()
	defer i.oauthMutex.Unlock()
	i.oauthProviders[identityProvider.Name] = provider
	return nil
}

// Identity represents the account mapped to kubesphere
type Identity interface {
	// GetUserID required
	// Identifier for the End-User at the Issuer.
	GetUserID() string
	// GetUsername optional
	// The username which the End-User wishes to be referred to kubesphere.
	GetUsername() string
	// GetEmail optional
	GetEmail() string
}

// RegisterOAuthProviderFactory register OAuthProviderFactory with the specified type
func RegisterOAuthProviderFactory(factory OAuthProviderFactory) {
	oauthProviderFactories[factory.Type()] = factory
}

// RegisterGenericProviderFactory registers GenericProviderFactory with the specified type
func RegisterGenericProviderFactory(factory GenericProviderFactory) {
	genericProviderFactories[factory.Type()] = factory
}

func GetGenericProviderFactory(providerType string) GenericProviderFactory {
	return genericProviderFactories[providerType]
}

func GetOAuthProviderFactory(providerType string) OAuthProviderFactory {
	return oauthProviderFactories[providerType]
}
