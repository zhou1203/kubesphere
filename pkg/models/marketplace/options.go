package marketplace

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/constants"
)

const ConfigurationFileKey = "configuration.yaml"
const ConfigurationSecretName = "marketplace"

type Options struct {
	URL                     string                  `json:"url" yaml:"url"`
	OAuthOptions            OAuthOptions            `json:"oauth" yaml:"oauth"`
	Account                 *Account                `json:"account,omitempty" yaml:"account"`
	SubscriptionSyncOptions SubscriptionSyncOptions `json:"subscription" yaml:"subscription"`
	RepositorySyncOptions   RepositorySyncOptions   `json:"repository" yaml:"repository"`
}

type Account struct {
	AccessToken  string    `json:"-" yaml:"accessToken"`
	UserID       string    `json:"userID" yaml:"userID"`
	ExpiresAt    time.Time `json:"expiresAt" yaml:"expiresAt"`
	Email        string    `json:"email" yaml:"email"`
	HeadImageURL string    `json:"headImageURL" yaml:"headImageURL"`
	Username     string    `json:"username" yaml:"username"`
}

type Bind struct {
	ClientID      string `json:"client_id" yaml:"clientID"`
	State         string `json:"state" yaml:"state"`
	CodeChallenge string `json:"code_challenge" yaml:"codeChallenge"`
	ClusterID     string `json:"cluster_id" yaml:"clusterID"`
	CodeVerifier  string `json:"-" yaml:"codeVerifier"`
}

type OAuthOptions struct {
	// https://kubesphere.cloud/auth/v1/auth
	// https://kubesphere.cloud/auth/v1/token
	// https://kubesphere.cloud/apis/user/v1/user
	ClientID     string `json:"clientID" yaml:"clientID"`
	ClientSecret string `json:"-" yaml:"clientSecret"`
	Bind         *Bind  `json:"-,omitempty" yaml:"bind,omitempty"`
}

type SubscriptionSyncOptions struct {
	// https://clouddev.kubesphere.io/subscribe/465763320050230423
	// https://clouddev.kubesphere.io/subscribe/465763320050230423/apis/extension/v1/users/{user_id}/extensions/subscriptions/search
	SyncPeriod time.Duration `json:"syncPeriod" yaml:"syncPeriod"`
}

type BasicAuth struct {
	Username string `json:"username,omitempty" yaml:"username"`
	Password string `json:"password,omitempty" yaml:"password"`
}

type RepositorySyncOptions struct {
	// https://clouddev.kubesphere.io/apis/extension/v1/categories
	// https://clouddev.kubesphere.io/apis/extension/v1/extensions/search
	URL string `json:"url" yaml:"url"`
	// https://app.clouddev.kubesphere.io
	SyncPeriod time.Duration `json:"syncPeriod" yaml:"syncPeriod"`
	RepoName   string        `json:"repoName" yaml:"repoName"`
	BasicAuth  *BasicAuth    `json:"-" yaml:"basicAuth"`
}

func LoadOptions(ctx context.Context, client runtimeclient.Client) (*Options, error) {
	optionsSecret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: ConfigurationSecretName}, optionsSecret); err != nil {
		if errors.IsNotFound(err) {
			return nil, errors.NewBadRequest("marketplace configuration not exists")
		}
		return nil, fmt.Errorf("failed to load marketplace configuration: %s", err)
	}
	options := &Options{}
	if err := yaml.NewDecoder(bytes.NewReader(optionsSecret.Data[ConfigurationFileKey])).Decode(options); err != nil {
		return nil, fmt.Errorf("failed to decode marketplace configuration: %s", err)
	}
	return options, nil
}

func SaveOptions(ctx context.Context, client runtimeclient.Client, options *Options) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		optionsSecret := &corev1.Secret{}
		if err := client.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: ConfigurationSecretName}, optionsSecret); err != nil {
			if errors.IsNotFound(err) {
				return errors.NewBadRequest("marketplace configuration not exists")
			}
			return fmt.Errorf("failed to load marketplace configuration: %s", err)
		}
		data, err := yaml.Marshal(options)
		if err != nil {
			return fmt.Errorf("failed to encode marketplace configuration: %s", err)
		}

		optionsSecret.Data[ConfigurationFileKey] = data
		return client.Update(ctx, optionsSecret)
	})

	if err != nil {
		return fmt.Errorf("failed to update marketplace configuration: %s", err)
	}

	return nil
}
