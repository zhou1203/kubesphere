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

package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authuser "k8s.io/apiserver/pkg/authentication/user"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
)

var (
	RateLimitExceededError  = fmt.Errorf("auth rate limit exceeded")
	IncorrectPasswordError  = fmt.Errorf("incorrect password")
	AccountIsNotActiveError = fmt.Errorf("account is not active")
)

// PasswordAuthenticator is an interface implemented by authenticator which take a
// username ,password and provider. provider refers to the identity provider`s name,
// if the provider is empty, authenticate from kubesphere account. Note that implement this
// interface you should also obey the error specification errors.Error defined at package
// "k8s.io/apimachinery/pkg/api", and restful.ServerError defined at package
// "github.com/emicklei/go-restful/v3", or the server cannot handle error correctly.
type PasswordAuthenticator interface {
	Authenticate(ctx context.Context, provider, username, password string) (authuser.Info, error)
}

// OAuthAuthenticator authenticate users by OAuth 2.0 Authorization Framework. Note that implement this
// interface you should also obey the error specification errors.Error defined at package
// "k8s.io/apimachinery/pkg/api", and restful.ServerError defined at package
// "github.com/emicklei/go-restful/v3", or the server cannot handle error correctly.
type OAuthAuthenticator interface {
	Authenticate(ctx context.Context, provider string, req *http.Request) (authuser.Info, error)
}

func otpAuthRequiredUser(user *iamv1beta1.User) (authuser.Info, error) {
	return &authuser.DefaultInfo{
		Name: iamv1beta1.OTPAuthRequiredUser,
		Extra: map[string][]string{
			iamv1beta1.ExtraUID:      {string(user.UID)},
			iamv1beta1.ExtraUsername: {user.Name},
		},
	}, nil
}

func preRegistrationUser(idp string, identity identityprovider.Identity) authuser.Info {
	return &authuser.DefaultInfo{
		Name: iamv1beta1.PreRegistrationUser,
		Extra: map[string][]string{
			iamv1beta1.ExtraIdentityProvider: {idp},
			iamv1beta1.ExtraUID:              {identity.GetUserID()},
			iamv1beta1.ExtraUsername:         {identity.GetUsername()},
			iamv1beta1.ExtraEmail:            {identity.GetEmail()},
		},
	}
}

func mappedUser(idp string, identity identityprovider.Identity) *iamv1beta1.User {
	// username convert
	username := strings.ToLower(identity.GetUsername())
	return &iamv1beta1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
			Labels: map[string]string{
				iamv1beta1.IdentifyProviderLabel: idp,
				iamv1beta1.OriginUIDLabel:        identity.GetUserID(),
			},
		},
		Spec: iamv1beta1.UserSpec{Email: identity.GetEmail()},
	}
}
