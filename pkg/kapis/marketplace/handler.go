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

package marketplace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/golang-jwt/jwt/v4"
	"k8s.io/client-go/util/retry"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	marketplacev1alpha1 "kubesphere.io/api/marketplace/v1alpha1"

	"kubesphere.io/kubesphere/pkg/models/marketplace"

	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

type handler struct {
	client runtimeclient.Client
}

type BindResponse struct {
}

func (h *handler) bind(request *restful.Request, response *restful.Response) {
	systemNamespace := &corev1.Namespace{}
	if err := h.client.Get(request.Request.Context(), types.NamespacedName{Name: constants.KubeSphereNamespace}, systemNamespace); err != nil {
		api.HandleInternalError(response, request, err)
		return
	}

	options, err := marketplace.LoadOptions(request.Request.Context(), h.client)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	state := rand.String(32)
	clusterID := string(systemNamespace.UID)
	codeVerifier := rand.String(32)

	hashData := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := hex.EncodeToString(hashData[:])

	bind := &marketplace.Bind{
		ClientID:      options.OAuthOptions.ClientID,
		State:         state,
		CodeChallenge: codeChallenge,
		ClusterID:     clusterID,
		CodeVerifier:  codeVerifier,
	}

	options.OAuthOptions.Bind = bind
	if err := marketplace.SaveOptions(request.Request.Context(), h.client, options); err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(bind)
}

func (h *handler) callback(request *restful.Request, response *restful.Response) {
	code := request.QueryParameter("code")
	state := request.QueryParameter("state")
	clusterID := request.QueryParameter("cluster_id")

	options, err := marketplace.LoadOptions(request.Request.Context(), h.client)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}

	bind := options.OAuthOptions.Bind
	if bind == nil || state != bind.State {
		api.HandleBadRequest(response, request, fmt.Errorf("state mismatch"))
		return
	}

	if bind.ClusterID != clusterID {
		api.HandleBadRequest(response, request, fmt.Errorf("cluster_id mismatch"))
		return
	}

	client := marketplace.NewClient(options)
	token, err := client.CreateToken(clusterID, code, bind.CodeVerifier)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	userInfo, err := client.UserInfo(token.AccessToken)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	claims := &jwt.RegisteredClaims{}
	_, _, err = jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(token.AccessToken, claims)
	if err != nil {
		api.HandleError(response, request, fmt.Errorf("failed to parse access token: %s", err))
		return
	}
	options.Account = &marketplace.Account{
		AccessToken:  token.AccessToken,
		ExpiresAt:    claims.ExpiresAt.Time,
		UserID:       userInfo.ID,
		Username:     userInfo.Username,
		Email:        userInfo.Email,
		HeadImageURL: userInfo.HeadImageURL,
	}
	options.OAuthOptions.Bind = nil
	if err := marketplace.SaveOptions(request.Request.Context(), h.client, options); err != nil {
		api.HandleError(response, request, err)
		return
	}

	_ = response.WriteEntity(errors.None)
}

func (h *handler) unbind(request *restful.Request, response *restful.Response) {
	options, err := marketplace.LoadOptions(request.Request.Context(), h.client)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	options.Account = nil
	if err := marketplace.SaveOptions(request.Request.Context(), h.client, options); err != nil {
		api.HandleError(response, request, err)
		return
	}
	_ = response.WriteEntity(errors.None)
}

func (h *handler) sync(request *restful.Request, response *restful.Response) {
	options, err := marketplace.LoadOptions(request.Request.Context(), h.client)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	client := marketplace.NewClient(options)
	if err != nil {
		api.HandleError(response, request, err)
		return
	}
	subscriptions, err := client.ListSubscriptions("")
	if err != nil {
		if marketplace.IsForbiddenError(err) {
			options.Account = nil
			if err := marketplace.SaveOptions(request.Request.Context(), h.client, options); err != nil {
				api.HandleError(response, request, fmt.Errorf("failed to update marketplace options: %s", err))
				return
			}
		}
		api.HandleError(response, request, fmt.Errorf("failed to list subscriptions: %s", err))
		return
	}

	for _, subscription := range subscriptions {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			extensions := &corev1alpha1.ExtensionList{}
			if err := h.client.List(request.Request.Context(), extensions,
				runtimeclient.MatchingLabels{marketplacev1alpha1.ExtensionID: subscription.ExtensionID}); err != nil {
				return err
			}
			if len(extensions.Items) > 0 {
				extension := extensions.Items[0]
				if extension.Labels[marketplacev1alpha1.Subscribed] != "true" {
					extension.Labels[marketplacev1alpha1.Subscribed] = "true"
					if err := h.client.Update(request.Request.Context(), &extension); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			api.HandleError(response, request, fmt.Errorf("failed to sync extension status: %s", err))
			return
		}
	}
	_ = response.WriteEntity(errors.None)
}
