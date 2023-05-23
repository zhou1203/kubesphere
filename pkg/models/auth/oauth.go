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
	"net/http"

	"k8s.io/apimachinery/pkg/api/errors"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/oauth"
)

type oauthAuthenticator struct {
	client     runtimeclient.Client
	userGetter *userMapper
	options    *authentication.Options
}

func NewOAuthAuthenticator(cacheClient runtimeclient.Client, options *authentication.Options) OAuthAuthenticator {
	authenticator := &oauthAuthenticator{
		client:     cacheClient,
		userGetter: &userMapper{cache: cacheClient},
		options:    options,
	}
	return authenticator
}

func (o *oauthAuthenticator) Authenticate(_ context.Context, provider string, req *http.Request) (authuser.Info, string, error) {
	providerOptions, err := o.options.OAuthOptions.IdentityProviderOptions(provider)
	// identity provider not registered
	if err != nil {
		klog.Error(err)
		return nil, "", err
	}
	oauthIdentityProvider, err := identityprovider.GetOAuthProvider(providerOptions.Name)
	if err != nil {
		klog.Error(err)
		return nil, "", err
	}
	authenticated, err := oauthIdentityProvider.IdentityExchangeCallback(req)
	if err != nil {
		klog.Error(err)
		return nil, "", err
	}

	user, err := o.userGetter.FindMappedUser(providerOptions.Name, authenticated.GetUserID())
	if user == nil && providerOptions.MappingMethod == oauth.MappingMethodLookup {
		klog.Error(err)
		return nil, "", err
	}

	// the user will automatically create and mapping when login successful.
	if user == nil && providerOptions.MappingMethod == oauth.MappingMethodAuto {
		if !providerOptions.DisableLoginConfirmation {
			return preRegistrationUser(providerOptions.Name, authenticated), providerOptions.Name, nil
		}

		user = mappedUser(providerOptions.Name, authenticated)

		if err = o.client.Create(context.Background(), user); err != nil {
			return nil, providerOptions.Name, err
		}
	}

	if user != nil {
		if user.Status.State == iamv1beta1.UserDisabled {
			// state not active
			return nil, "", AccountIsNotActiveError
		}
		return &authuser.DefaultInfo{Name: user.GetName()}, providerOptions.Name, nil
	}

	return nil, "", errors.NewNotFound(iamv1beta1.Resource("user"), authenticated.GetUsername())
}
