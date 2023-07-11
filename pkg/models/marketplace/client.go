package marketplace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"

	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

type Interface interface {
	ExtensionID(extensionName string) (string, error)
	ListSubscriptions() ([]Subscription, error)
	CreateToken(clusterID, code, codeVerifier string) (*Token, error)
	UserInfo() (*UserInfo, error)
}

type client struct {
	options    *Options
	httpClient *http.Client
}

type Paginator struct {
	Desc       bool   `json:"desc"`
	OrderBy    string `json:"order_by"`
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
	TotalCount int    `json:"total_count"`
	TotalPages int    `json:"total_pages"`
}

type ExtensionList struct {
	Paginator  Paginator   `json:"paginator"`
	Extensions []Extension `json:"extensions"`
}

type Extension struct {
	ExtensionID string `json:"extension_id"`
	Name        string `json:"name"`
}

type SubscriptionList struct {
	Paginator     Paginator      `json:"paginator"`
	Subscriptions []Subscription `json:"subscriptions"`
}

type Token struct {
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	AccessToken string `json:"access_token"`
}

type UserInfo struct {
	Username     string `json:"name"`
	ID           string `json:"user_id"`
	Email        string `json:"email"`
	HeadImageURL string `json:"head_image_url"`
}

type Subscription struct {
	ClusterID          string `json:"cluster_id"`
	CreatedAt          string `json:"created_at"`
	DeletedAt          string `json:"deleted_at"`
	ExpiredAt          string `json:"expired_at"`
	ExtensionID        string `json:"extension_id"`
	ExtraInfo          string `json:"extra_info"`
	OrderID            string `json:"order_id"`
	StartedAt          string `json:"started_at"`
	SubscriptionID     string `json:"subscription_id"`
	UpdatedAt          string `json:"updated_at"`
	UserID             string `json:"user_id"`
	UserSubscriptionID string `json:"user_subscription_id"`
}

func (c *client) ExtensionID(extensionName string) (string, error) {
	var extensionID string
	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return true
	}, func() error {
		body := strings.NewReader(fmt.Sprintf("{\"paginator\":{\"page\":1},\"names\":[\"%s\"]}", extensionName))
		resp, err := c.httpClient.Post(fmt.Sprintf("%s/apis/extension/v1/extensions/search", c.options.URL), "application/json", body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		result := &ExtensionList{}
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return err
		}
		if len(result.Extensions) == 1 && result.Extensions[0].Name == extensionName {
			extensionID = result.Extensions[0].ExtensionID
		} else {
			klog.V(4).Infof("extensionID not exists %s: %v", extensionName, result.Extensions)
		}
		return nil
	})

	return extensionID, err
}

func (c *client) UserInfo() (*UserInfo, error) {
	var userInfo *UserInfo
	err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return true
	}, func() error {
		userInfoReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/apis/user/v1/user", c.options.URL), nil)
		if err != nil {
			return fmt.Errorf("failed to create user info request: %s", err)
		}
		userInfoReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
		userInfoResp, err := c.httpClient.Do(userInfoReq)
		if err != nil {
			return fmt.Errorf("failed to exchange access token: %s", err)
		}
		defer userInfoResp.Body.Close()
		if err := json.NewDecoder(userInfoResp.Body).Decode(userInfo); err != nil {
			return fmt.Errorf("failed to decode userInfo response: %s", err)
		}
		return nil
	})
	return userInfo, err
}

func (c *client) ListSubscriptions() ([]Subscription, error) {
	page := 1
	subscriptions := make([]Subscription, 0)
	for {
		result := &SubscriptionList{}
		err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return true
		}, func() error {
			body := strings.NewReader(fmt.Sprintf("{\"paginator\":{\"page\":%d}}", page))
			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/apis/extension/v1/users/%s/extensions/subscriptions/search", c.options.URL, c.options.Account.UserID), body)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.options.Account.AccessToken))
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.httpClient.Do(req)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err = json.NewDecoder(io.NopCloser(bytes.NewBuffer(data))).Decode(result); err != nil {
				return err
			}
			subscriptions = append(subscriptions, result.Subscriptions...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if page >= result.Paginator.TotalPages {
			break
		}
		page++
	}
	return subscriptions, nil
}

func (c *client) CreateToken(clusterID, code, codeVerifier string) (*Token, error) {
	values := url.Values{}
	values.Add("cluster_id", clusterID)
	values.Add("code", code)
	values.Add("code_verifier", codeVerifier)
	values.Add("client_id", c.options.OAuthOptions.ClientID)
	values.Add("client_secret", c.options.OAuthOptions.ClientSecret)
	values.Add("grant_type", "authorization_code")

	tokenResp, err := http.PostForm(fmt.Sprintf("%s/apis/auth/v1/token", c.options.URL), values)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange access token: %s", err)
	}
	defer tokenResp.Body.Close()

	token := &Token{}
	if err := json.NewDecoder(tokenResp.Body).Decode(token); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %s", err)
	}

	return token, nil
}

func NewClient(options *Options) Interface {
	return &client{options: options, httpClient: http.DefaultClient}
}
