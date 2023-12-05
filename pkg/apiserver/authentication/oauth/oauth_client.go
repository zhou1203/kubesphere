package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	v1 "k8s.io/api/core/v1"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	GrantMethodAuto   = "auto"
	GrantMethodPrompt = "prompt"
	GrantMethodDeny   = "deny"
	GrantMethodNone   = ""

	SecretTypeOAuthClient = "config.kubesphere.io/oauthclient"

	SecretDataKey         = "configuration.yaml"
	SecretLabelClientName = "config.kubesphere.io/oauthclient-name"
	SecretLabelConfigType = "config.kubesphere.io/type"
)

var (
	ErrorClientNotFound        = errors.New("the OAuth client was not found")
	ErrorRedirectURLNotAllowed = errors.New("redirect URL is not allowed")

	// AllowAllRedirectURI Allow any redirect URI if the redirectURI is defined in request
	AllowAllRedirectURI = "*"

	ValidGrantMethods = []string{GrantMethodAuto, GrantMethodPrompt, GrantMethodDeny}
)

type Client struct {
	// The name of the OAuth client is used as the client_id parameter when making requests to <master>/oauth/authorize
	Name string `yaml:"name"`

	// Secret is the unique secret associated with a client
	Secret string `yaml:"secret"`

	// GrantMethod determines how to handle grants for this client. If no method is provided, the
	// cluster default grant handling method will be used. Valid grant handling methods are:
	//  - auto:   always approves grant requests, useful for trusted clients
	//  - prompt: prompts the end user for approval of grant requests, useful for third-party clients
	//  - deny:   always denies grant requests, useful for black-listed clients
	GrantMethod string `yaml:"grantMethod"`

	// RespondWithChallenges indicates whether the client wants authentication needed responses made
	// in the form of challenges instead of redirects
	RespondWithChallenges bool `yaml:"respondWithChallenges,omitempty"`

	// ScopeRestrictions describes which scopes this client can request.  Each requested scope
	// is checked against each restriction.  If any restriction matches, then the scope is allowed.
	// If no restriction matches, then the scope is denied.
	ScopeRestrictions []string `yaml:"scopeRestrictions,omitempty"`

	// RedirectURIs is the valid redirection URIs associated with a client
	RedirectURIs []string `yaml:"redirectURIs,omitempty"`

	// AccessTokenMaxAge overrides the default access token max age for tokens granted to this client.
	// Default value is 7200, the minimum 600
	AccessTokenMaxAge int64 `yaml:"accessTokenMaxAge,omitempty"`

	// AccessTokenInactivityTimeout overrides the default token
	// inactivity timeout for tokens granted to this client.
	AccessTokenInactivityTimeout int64 `yaml:"accessTokenInactivityTimeout,omitempty"`
}

type ClientGetter interface {
	GetOAuthClient(ctx context.Context, name string) (*Client, error)
}

func NewOAuthClientGetter(reader client.Reader) ClientGetter {
	return &oauthClientGetter{reader}
}

type oauthClientGetter struct {
	client.Reader
}

func (o *oauthClientGetter) GetOAuthClient(ctx context.Context, name string) (*Client, error) {
	oauthClient := &Client{}
	secrets := &v1.SecretList{}
	err := o.List(ctx, secrets, client.MatchingLabels{SecretLabelClientName: name})
	if err != nil {
		return nil, err
	}

	if len(secrets.Items) == 0 {
		return nil, ErrorClientNotFound
	}

	// select the first item, because we ensure there will not be a client with the same name
	secret := secrets.Items[0]

	if secret.Type != SecretTypeOAuthClient {
		return nil, ErrorClientNotFound
	}

	if err = yaml.Unmarshal(secret.Data[SecretDataKey], oauthClient); err != nil {
		return nil, err
	}

	return oauthClient, nil
}

func ValidateClient(client Client) error {
	var es []error
	if !sliceutil.HasString(ValidGrantMethods, client.GrantMethod) {
		es = append(es, fmt.Errorf("invalid grant method: %s", client.GrantMethod))
	}
	if client.AccessTokenInactivityTimeout != 0 &&
		client.AccessTokenInactivityTimeout < 600 {
		es = append(es, fmt.Errorf("invalid access token inactivity timeout: %d, The minimum value can only be 600", client.AccessTokenInactivityTimeout))
	}
	if client.AccessTokenMaxAge != 0 &&
		client.AccessTokenMaxAge < 600 {
		es = append(es, fmt.Errorf("invalid access token max age: %d, The minimum value can only be 600", client.AccessTokenInactivityTimeout))
	}

	return errorsutil.NewAggregate(es)
}

func (c *Client) ResolveRedirectURL(expectURL string) (*url.URL, error) {
	// RedirectURIs is empty
	if len(c.RedirectURIs) == 0 {
		return nil, ErrorRedirectURLNotAllowed
	}
	allowAllRedirectURI := sliceutil.HasString(c.RedirectURIs, AllowAllRedirectURI)
	redirectAbleURIs := anyRedirectAbleURI(c.RedirectURIs)

	if expectURL == "" {
		// Need to specify at least one RedirectURI
		if len(redirectAbleURIs) > 0 {
			return url.Parse(redirectAbleURIs[0])
		} else {
			return nil, ErrorRedirectURLNotAllowed
		}
	}
	if allowAllRedirectURI || sliceutil.HasString(redirectAbleURIs, expectURL) {
		return url.Parse(expectURL)
	}
	return nil, ErrorRedirectURLNotAllowed
}

func (c *Client) IsValidScope(requestedScope string) bool {
	for _, s := range strings.Split(requestedScope, " ") {
		if !sliceutil.HasString(c.ScopeRestrictions, s) {
			return false
		}
	}
	return true
}

func anyRedirectAbleURI(redirectURIs []string) []string {
	uris := make([]string, 0)
	for _, uri := range redirectURIs {
		_, err := url.Parse(uri)
		if err == nil {
			uris = append(uris, uri)
		}
	}
	return uris
}

func UnmarshalTo(secret v1.Secret) (*Client, error) {
	oc := &Client{}
	err := yaml.Unmarshal(secret.Data[SecretDataKey], oc)
	if err != nil {
		return nil, err
	}
	return oc, nil
}
