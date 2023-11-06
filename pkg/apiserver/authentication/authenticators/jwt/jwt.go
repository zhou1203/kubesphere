/*
Copyright 2019 The KubeSphere Authors.

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

package jwt

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	"kubesphere.io/kubesphere/pkg/models/auth"
	"kubesphere.io/kubesphere/pkg/utils/serviceaccount"
)

// TokenAuthenticator implements kubernetes token authenticate interface with our custom logic.
// TokenAuthenticator will retrieve user info from cache by given token. If empty or invalid token
// was given, authenticator will still give passed response at the condition user will be user.Anonymous
// and group from user.AllUnauthenticated. This helps requests be passed along the handler chain,
// because some resources are public accessible.
type tokenAuthenticator struct {
	tokenOperator auth.TokenManagementInterface
	cache         runtimecache.Cache
}

func NewTokenAuthenticator(cache runtimecache.Cache, tokenOperator auth.TokenManagementInterface) authenticator.Token {
	return &tokenAuthenticator{
		tokenOperator: tokenOperator,
		cache:         cache,
	}
}

func (t *tokenAuthenticator) AuthenticateToken(ctx context.Context, token string) (*authenticator.Response, bool, error) {
	verified, err := t.tokenOperator.Verify(token)
	if err != nil {
		klog.Warning(err)
		return nil, false, err
	}

	if serviceaccount.IsServiceAccountToken(verified.Subject) {
		_, err = t.validateServiceAccount(verified)
		if err != nil {
			return nil, false, err
		}

		return &authenticator.Response{
			User: verified.User,
		}, true, nil

	}

	if verified.User.GetName() == iamv1beta1.PreRegistrationUser {
		return &authenticator.Response{
			User: verified.User,
		}, true, nil
	}

	userInfo := &iamv1beta1.User{}
	if err := t.cache.Get(ctx, types.NamespacedName{Name: verified.User.GetName()}, userInfo); err != nil {
		return nil, false, err
	}

	// AuthLimitExceeded state should be ignored
	if userInfo.Status.State == iamv1beta1.UserDisabled {
		return nil, false, auth.AccountIsNotActiveError
	}
	return &authenticator.Response{
		User: &user.DefaultInfo{
			Name:   userInfo.GetName(),
			Groups: append(userInfo.Spec.Groups, user.AllAuthenticated),
		},
	}, true, nil
}

func (t *tokenAuthenticator) validateServiceAccount(verify *token.VerifiedResponse) (*corev1alpha1.ServiceAccount, error) {
	// Ensure the relative service account exist
	name, namespace := serviceaccount.SplitUsername(verify.Username)
	sa := &corev1alpha1.ServiceAccount{}
	ctx := context.Background()
	err := t.cache.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sa)
	if err != nil {
		return nil, err
	}
	return sa, nil
}
