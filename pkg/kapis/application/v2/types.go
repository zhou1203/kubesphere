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

package v2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
)

type CreateAppRequest struct {
	CategoryID  string `json:"category_id,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Name        string `json:"name,omitempty"`
	AppType     string `json:"app_type,omitempty"`
	Package     []byte `json:"package,omitempty"`
	VersionName string `json:"version_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type CreateAppVersionRequest struct {
	AppId       string `json:"app_id,omitempty"`
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	AppType     string `json:"app_type,omitempty"`
	Package     []byte `json:"package,omitempty"`
	VersionName string `json:"version_name,omitempty"`
}

type AppResp struct {
	Name                string               `json:"name,omitempty"`
	DisplayName         corev1alpha1.Locales `json:"display_name,omitempty"`
	AppType             string               `json:"app_type,omitempty"`
	CategoryDisplayName corev1alpha1.Locales `json:"category_display_name,omitempty"`
	Workspace           string               `json:"workspace,omitempty"`
	Description         corev1alpha1.Locales `json:"description,omitempty"`
	Icon                string               `json:"icon,omitempty"`
	LatestAppVersion    string               `json:"latest_app_version,omitempty"`
	Maintainers         string               `json:"maintainers,omitempty"`
	State               string               `json:"state,omitempty"`
	UpdateTime          *metav1.Time         `json:"update_time,omitempty"`
}

const (
	Status = "status"
)
