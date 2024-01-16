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
	"net/http"

	"k8s.io/apimachinery/pkg/api/errors"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
)

type oauthAuthenticator struct {
	client                 runtimeclient.Client
	userGetter             *userMapper
	idpHandler             identityprovider.Handler
	idpConfigurationGetter identityprovider.ConfigurationGetter
}

func NewOAuthAuthenticator(cacheClient runtimeclient.Client, idpHandler identityprovider.Handler) OAuthAuthenticator {
	authenticator := &oauthAuthenticator{
		client:                 cacheClient,
		userGetter:             &userMapper{cache: cacheClient},
		idpHandler:             idpHandler,
		idpConfigurationGetter: identityprovider.NewConfigurationGetter(cacheClient),
	}
	return authenticator
}

func (o *oauthAuthenticator) Authenticate(ctx context.Context, provider string, req *http.Request) (authuser.Info, error) {
	providerConfig, err := o.idpConfigurationGetter.GetConfiguration(ctx, provider)
	// identity provider not registered
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	oauthIdentityProvider, exist := o.idpHandler.GetOAuthProvider(provider)
	if !exist {
		return nil, fmt.Errorf("identity provider %s not exist", provider)
	}
	authenticated, err := oauthIdentityProvider.IdentityExchangeCallback(req)
	if err != nil {
		klog.Errorf("Failed to exchange identity from %s, error: %v", provider, err)
		return nil, err
	}

	user, err := o.userGetter.FindMappedUser(ctx, providerConfig.Name, authenticated.GetUserID())
	if user == nil && providerConfig.MappingMethod == identityprovider.MappingMethodLookup {
		klog.Errorf("Failed to find mapped user for %s, error: %v", provider, err)
		return nil, err
	}

	// the user will automatically create and mapping when login successful.
	if user == nil && providerConfig.MappingMethod == identityprovider.MappingMethodAuto {
		if !providerConfig.DisableLoginConfirmation {
			return preRegistrationUser(providerConfig.Name, authenticated), nil
		}

		user = mappedUser(providerConfig.Name, authenticated)

		if err = o.client.Create(ctx, user); err != nil {
			return nil, err
		}
	}

	if user != nil {
		if user.Status.State == iamv1beta1.UserDisabled {
			// state not active
			return nil, AccountIsNotActiveError
		}
		if user.Annotations[TOTPAuthKeyRefAnnotation] != "" {
			return otpAuthRequiredUser(user)
		}
		return &authuser.DefaultInfo{Name: user.GetName()}, nil
	}

	return nil, errors.NewNotFound(iamv1beta1.Resource("user"), authenticated.GetUsername())
}
