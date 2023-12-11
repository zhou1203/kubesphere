package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	v1 "k8s.io/api/core/v1"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	GrantMethodAuto   = "auto"
	GrantMethodPrompt = "prompt"
	GrantMethodDeny   = "deny"

	SecretTypeOAuthClient = "config.kubesphere.io/oauthclient"

	SecretDataKey         = "configuration.yaml"
	SecretLabelClientName = "config.kubesphere.io/oauthclient-name"
	SecretLabelConfigType = "config.kubesphere.io/type"
)

var (
	ErrorClientNotFound        = errors.New("the OAuth client was not found")
	ErrorRedirectURLNotAllowed = errors.New("redirect URL is not allowed")

	ValidGrantMethods = []string{GrantMethodAuto, GrantMethodPrompt, GrantMethodDeny}
)

// Client represents an OAuth client configuration.
type Client struct {
	// Name is the unique identifier for the OAuth client. It is used as the client_id parameter
	// when making requests to <master>/oauth/authorize.
	Name string `yaml:"name"`

	// Secret is the unique secret associated with the client for secure communication.
	Secret string `yaml:"secret"`

	// Trusted indicates whether the client is considered a trusted client.
	Trusted bool `yaml:"trusted"`

	// GrantMethod determines how grant requests for this client should be handled. If no method is provided,
	// the cluster default grant handling method will be used. Valid grant handling methods are:
	//   - auto:   Always approves grant requests, useful for trusted clients.
	//   - prompt: Prompts the end user for approval of grant requests, useful for third-party clients.
	//   - deny:   Always denies grant requests, useful for black-listed clients.
	GrantMethod string `yaml:"grantMethod"`

	// RespondWithChallenges indicates whether the client prefers authentication needed responses
	// in the form of challenges instead of redirects.
	RespondWithChallenges bool `yaml:"respondWithChallenges,omitempty"`

	// ScopeRestrictions describes which scopes this client can request. Each requested scope
	// is checked against each restriction. If any restriction matches, then the scope is allowed.
	// If no restriction matches, then the scope is denied.
	ScopeRestrictions []string `yaml:"scopeRestrictions,omitempty"`

	// RedirectURIs is a list of valid redirection URIs associated with the client.
	RedirectURIs []string `yaml:"redirectURIs,omitempty"`

	// AccessTokenMaxAge overrides the default maximum age for access tokens granted to this client.
	// The default value is 7200 seconds, and the minimum allowed value is 600 seconds.
	AccessTokenMaxAge int64 `yaml:"accessTokenMaxAge,omitempty"`

	// AccessTokenInactivityTimeout overrides the default token inactivity timeout
	// for tokens granted to this client.
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

// GetOAuthClient retrieves an OAuth client by name from the underlying storage.
// It returns the OAuth client if found; otherwise, returns an error.
func (o *oauthClientGetter) GetOAuthClient(ctx context.Context, name string) (*Client, error) {

	// Query for secrets with matching client name labels.
	secrets := &v1.SecretList{}
	err := o.List(ctx, secrets, client.MatchingLabels{SecretLabelClientName: name})
	if err != nil {
		return nil, err
	}

	// Check if any secrets with matching client name were found.
	if len(secrets.Items) == 0 {
		return nil, ErrorClientNotFound
	}

	// Select the first item, as there should not be multiple clients with the same name.
	secret := secrets.Items[0]

	// Verify the secret type is OAuthClient.
	if secret.Type != SecretTypeOAuthClient {
		return nil, ErrorClientNotFound
	}

	// Return the retrieved OAuth client.
	return UnmarshalFrom(&secret)
}

// ValidateClient validates the properties of the provided OAuth 2.0 client.
// It checks the client's grant method, access token inactivity timeout, and access
// token max age for validity. If any validation fails, it returns an aggregated error.
func ValidateClient(client Client) error {
	var validationErrors []error

	// Validate grant method.
	if !sliceutil.HasString(ValidGrantMethods, client.GrantMethod) {
		validationErrors = append(validationErrors, fmt.Errorf("invalid grant method: %s", client.GrantMethod))
	}

	// Validate access token inactivity timeout.
	if client.AccessTokenInactivityTimeout != 0 && client.AccessTokenInactivityTimeout < 600 {
		validationErrors = append(validationErrors, fmt.Errorf("invalid access token inactivity timeout: %d, the minimum value can only be 600", client.AccessTokenInactivityTimeout))
	}

	// Validate access token max age.
	if client.AccessTokenMaxAge != 0 && client.AccessTokenMaxAge < 600 {
		validationErrors = append(validationErrors, fmt.Errorf("invalid access token max age: %d, the minimum value can only be 600", client.AccessTokenMaxAge))
	}

	// Aggregate validation errors and return.
	return errorsutil.NewAggregate(validationErrors)
}

// ResolveRedirectURL resolves the redirect URL for the OAuth 2.0 authorization process.
// It takes an expected URL as a parameter and returns the resolved URL if it's allowed.
// If the expected URL is not provided, it uses the first available RedirectURI from the client.
func (c *Client) ResolveRedirectURL(expectURL string) (*url.URL, error) {
	// Check if RedirectURIs are specified for the client.
	if len(c.RedirectURIs) == 0 {
		return nil, ErrorRedirectURLNotAllowed
	}

	// Get the list of redirectable URIs for the client.
	redirectAbleURIs := filterValidRedirectURIs(c.RedirectURIs)

	// If the expected URL is not provided, use the first available RedirectURI.
	if expectURL == "" {
		if len(redirectAbleURIs) > 0 {
			return url.Parse(redirectAbleURIs[0])
		} else {
			// No RedirectURIs available for the client.
			return nil, ErrorRedirectURLNotAllowed
		}
	}

	// Check if the provided expected URL is allowed.
	if sliceutil.HasString(redirectAbleURIs, expectURL) {
		return url.Parse(expectURL)
	}

	// The provided expected URL is not allowed.
	return nil, ErrorRedirectURLNotAllowed
}

// IsValidScope checks whether the requested scope is valid for the client.
// It compares each individual scope in the requested scope string with the client's
// allowed scope restrictions. If all scopes are allowed, it returns true; otherwise, false.
func (c *Client) IsValidScope(requestedScope string) bool {
	// Split the requested scope string into individual scopes.
	scopes := strings.Split(requestedScope, " ")

	// Check each individual scope against the client's scope restrictions.
	for _, scope := range scopes {
		if !sliceutil.HasString(c.ScopeRestrictions, scope) {
			// Log a message indicating the disallowed scope.
			klog.V(4).Infof("Invalid scope: %s is not allowed for client %s", scope, c.Name)
			return false
		}
	}

	// All scopes are valid.
	return true
}

// filterValidRedirectURIs filters out invalid redirect URIs from the given slice.
// It returns a new slice containing only valid URIs.
func filterValidRedirectURIs(redirectURIs []string) []string {
	validURIs := make([]string, 0)
	for _, uri := range redirectURIs {
		// Check if the URI is valid by attempting to parse it.
		_, err := url.Parse(uri)
		if err == nil {
			// The URI is valid, add it to the list of valid URIs.
			validURIs = append(validURIs, uri)
		}
	}
	return validURIs
}

func UnmarshalFrom(secret *v1.Secret) (*Client, error) {
	oc := &Client{}
	if err := yaml.Unmarshal(secret.Data[SecretDataKey], oc); err != nil {
		return nil, err
	}
	return oc, nil
}

func MarshalInto(client *Client, secret *v1.Secret) error {
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[SecretLabelClientName] = client.Name
	secret.Labels[SecretLabelConfigType] = SecretTypeOAuthClient
	data, err := yaml.Marshal(client)
	if err != nil {
		return err
	}
	secret.Data = map[string][]byte{SecretDataKey: data}
	return nil
}
