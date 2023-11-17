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
	"reflect"
	"testing"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	runtimefakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/server/options"
)

func Test_oauthAuthenticator_Authenticate(t *testing.T) {
	var idpHandler = identityprovider.NewHandler()
	fakeIDP := &identityprovider.Configuration{
		Name:                     "fake",
		MappingMethod:            "auto",
		DisableLoginConfirmation: false,
		Type:                     "FakeIdentityProvider",
		ProviderOptions: options.DynamicOptions{
			"identities": map[string]interface{}{
				"code1": map[string]string{
					"uid":      "100001",
					"email":    "user1@kubesphere.io",
					"username": "user1",
				},
				"code2": map[string]string{
					"uid":      "100002",
					"email":    "user2@kubesphere.io",
					"username": "user2",
				},
			},
		},
	}

	marshal, err := yaml.Marshal(fakeIDP)
	if err != nil {
		return
	}

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-fake-idp",
			Namespace: "kubesphere-system",
			Labels: map[string]string{
				identityprovider.SecretLabelIdentityProviderName: "fake",
			},
		},
		Data: map[string][]byte{
			"configuration.yaml": marshal,
		},
		Type: identityprovider.SecretTypeIdentityProvider,
	}

	identityprovider.RegisterOAuthProviderFactory(&fakeProviderFactory{})
	err = idpHandler.RegisterOAuthProvider(fakeIDP)
	if err != nil {
		t.Fatal(err)
	}

	client := runtimefakeclient.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithRuntimeObjects(newUser("user1", "100001", "fake")).
		Build()

	err = client.Create(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}

	blockedUser := newUser("user2", "100002", "fake")
	blockedUser.Status = iamv1beta1.UserStatus{State: iamv1beta1.UserDisabled}
	if err := client.Create(context.Background(), blockedUser); err != nil {
		t.Fatal(err)
	}

	type args struct {
		ctx      context.Context
		provider string
		req      *http.Request
	}
	tests := []struct {
		name               string
		oauthAuthenticator OAuthAuthenticator
		args               args
		userInfo           user.Info
		provider           string
		wantErr            bool
	}{
		{
			name:               "Should successfully",
			oauthAuthenticator: NewOAuthAuthenticator(client, idpHandler),
			args: args{
				ctx:      context.Background(),
				provider: "fake",
				req:      must(http.NewRequest(http.MethodGet, "https://ks-console.kubesphere.io/oauth/callback/test?code=code1&state=100001", nil)),
			},
			userInfo: &user.DefaultInfo{
				Name: "user1",
			},
			provider: "fake",
			wantErr:  false,
		},
		{
			name:               "Blocked user test",
			oauthAuthenticator: NewOAuthAuthenticator(client, idpHandler),
			args: args{
				ctx:      context.Background(),
				provider: "fake",
				req:      must(http.NewRequest(http.MethodGet, "https://ks-console.kubesphere.io/oauth/callback/test?code=code2&state=100002", nil)),
			},
			userInfo: nil,
			provider: "",
			wantErr:  true,
		},
		{
			name:               "Should successfully",
			oauthAuthenticator: NewOAuthAuthenticator(client, idpHandler),
			args: args{
				ctx:      context.Background(),
				provider: "fake1",
				req:      must(http.NewRequest(http.MethodGet, "https://ks-console.kubesphere.io/oauth/callback/test?code=code1&state=100001", nil)),
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			userInfo, provider, err := tt.oauthAuthenticator.Authenticate(tt.args.ctx, tt.args.provider, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Authenticate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(userInfo, tt.userInfo) {
				t.Errorf("Authenticate() got = %v, want %v", userInfo, tt.userInfo)
			}
			if provider != tt.provider {
				t.Errorf("Authenticate() got = %v, want %v", provider, tt.provider)
			}
		})
	}
}

func must(r *http.Request, err error) *http.Request {
	if err != nil {
		panic(err)
	}
	return r
}

func newUser(username string, uid string, idp string) *iamv1beta1.User {
	return &iamv1beta1.User{
		TypeMeta: metav1.TypeMeta{
			Kind:       iamv1beta1.ResourceKindUser,
			APIVersion: iamv1beta1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
			Labels: map[string]string{
				iamv1beta1.IdentifyProviderLabel: idp,
				iamv1beta1.OriginUIDLabel:        uid,
			},
		},
	}
}

type fakeProviderFactory struct {
}

type fakeProvider struct {
	Identities map[string]fakeIdentity `json:"identities"`
}

type fakeIdentity struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

func (f fakeIdentity) GetUserID() string {
	return f.UID
}

func (f fakeIdentity) GetUsername() string {
	return f.Username
}

func (f fakeIdentity) GetEmail() string {
	return f.Email
}

func (fakeProviderFactory) Type() string {
	return "FakeIdentityProvider"
}

func (fakeProviderFactory) Create(dynamicOptions options.DynamicOptions) (identityprovider.OAuthProvider, error) {
	var fakeProvider fakeProvider
	if err := mapstructure.Decode(dynamicOptions, &fakeProvider); err != nil {
		return nil, err
	}
	return &fakeProvider, nil
}

func (f fakeProvider) IdentityExchangeCallback(req *http.Request) (identityprovider.Identity, error) {
	code := req.URL.Query().Get("code")
	if identity, ok := f.Identities[code]; ok {
		return identity, nil
	}
	return nil, fmt.Errorf("authorization failed")
}
