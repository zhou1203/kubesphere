/*

 Copyright 2021 The KubeSphere Authors.

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

package auth

import (
	"context"
	"fmt"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"golang.org/x/crypto/bcrypt"
	"k8s.io/apimachinery/pkg/api/errors"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
)

type passwordAuthenticator struct {
	userGetter                          *userMapper
	client                              runtimeclient.Client
	authOptions                         *authentication.Options
	identityProviderHandler             identityprovider.Handler
	identityProviderConfigurationGetter identityprovider.ConfigurationGetter
}

func NewPasswordAuthenticator(cacheClient runtimeclient.Client, identityProviderHandler identityprovider.Handler, options *authentication.Options) PasswordAuthenticator {
	passwordAuthenticator := &passwordAuthenticator{
		client:                              cacheClient,
		userGetter:                          &userMapper{cache: cacheClient},
		identityProviderConfigurationGetter: identityprovider.NewConfigurationGetter(cacheClient),
		identityProviderHandler:             identityProviderHandler,
		authOptions:                         options,
	}
	return passwordAuthenticator
}

func (p *passwordAuthenticator) Authenticate(_ context.Context, provider, username, password string) (authuser.Info, string, error) {
	// empty username or password are not allowed
	if username == "" || password == "" {
		return nil, "", IncorrectPasswordError
	}
	if provider != "" {
		return p.authByProvider(provider, username, password)
	}
	return p.authByKubeSphere(username, password)
}

// authByKubeSphere authenticate by the kubesphere user
func (p *passwordAuthenticator) authByKubeSphere(username, password string) (authuser.Info, string, error) {
	user, err := p.userGetter.Find(username)
	if err != nil {
		// ignore not found error
		if !errors.IsNotFound(err) {
			klog.Error(err)
			return nil, "", err
		}
	}

	// check user status
	if user != nil && user.Status.State != iamv1beta1.UserActive {
		if user.Status.State == iamv1beta1.UserAuthLimitExceeded {
			klog.Errorf("%s, username: %s", RateLimitExceededError, username)
			return nil, "", RateLimitExceededError
		} else {
			// state not active
			klog.Errorf("%s, username: %s", AccountIsNotActiveError, username)
			return nil, "", AccountIsNotActiveError
		}
	}

	// if the password is not empty, means that the password has been reset, even if the user was mapping from IDP
	if user != nil && user.Spec.EncryptedPassword != "" {
		if err = PasswordVerify(user.Spec.EncryptedPassword, password); err != nil {
			klog.Error(err)
			return nil, "", err
		}
		u := &authuser.DefaultInfo{
			Name:   user.Name,
			Groups: user.Spec.Groups,
		}
		// check if the password is initialized
		if uninitialized := user.Annotations[iamv1beta1.UninitializedAnnotation]; uninitialized != "" {
			u.Extra = map[string][]string{
				iamv1beta1.ExtraUninitialized: {uninitialized},
			}
		}
		return u, "", nil
	}

	return nil, "", IncorrectPasswordError
}

// authByProvider authenticate by the third-party identity provider user
func (p *passwordAuthenticator) authByProvider(providerName, username, password string) (authuser.Info, string, error) {
	genericProvider, exist := p.identityProviderHandler.GetGenericProvider(providerName)
	if !exist {
		return nil, "", fmt.Errorf("provider not exist")
	}

	configuration, err := p.identityProviderConfigurationGetter.GetConfiguration(context.Background(), providerName)
	if err != nil {
		return nil, "", err
	}

	authenticated, err := genericProvider.Authenticate(username, password)
	if err != nil {
		klog.Error(err)
		if errors.IsUnauthorized(err) {
			return nil, "", IncorrectPasswordError
		}
		return nil, "", err
	}
	linkedAccount, err := p.userGetter.FindMappedUser(providerName, authenticated.GetUserID())
	if err != nil && !errors.IsNotFound(err) {
		klog.Error(err)
		return nil, "", err
	}
	if linkedAccount != nil {
		return &authuser.DefaultInfo{Name: linkedAccount.Name}, providerName, nil
	}

	// the user will automatically create and mapping when login successful.
	if configuration.MappingMethod == identityprovider.MappingMethodAuto {
		if !configuration.DisableLoginConfirmation {
			return preRegistrationUser(configuration.Name, authenticated), configuration.Name, nil
		}
		linkedAccount = mappedUser(configuration.Name, authenticated)
		if err = p.client.Create(context.Background(), linkedAccount); err != nil {
			klog.Error(err)
			return nil, "", err
		}
		return &authuser.DefaultInfo{Name: linkedAccount.Name}, providerName, nil
	}

	return nil, "", err
}

func PasswordVerify(encryptedPassword, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(encryptedPassword), []byte(password)); err != nil {
		return IncorrectPasswordError
	}
	return nil
}
