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

	"kubesphere.io/api/constants"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
)

// ApplicationVersionSpec defines the desired state of ApplicationVersion
type ApplicationVersionSpec struct {
	DisplayName corev1alpha1.Locales `json:"displayName"`
	Version     string               `json:"version"`
	Home        string               `json:"home,omitempty"`
	Icon        string               `json:"icon,omitempty"`
	Description corev1alpha1.Locales `json:"description,omitempty"`
	Sources     []string             `json:"sources,omitempty"`
	Created     *metav1.Time         `json:"created,omitempty"`
	Digest      string               `json:"digest,omitempty"`
	AppType     string               `json:"appType,omitempty"`
	Maintainer  []Maintainer         `json:"maintainer,omitempty"`
}

// ApplicationVersionStatus defines the observed state of ApplicationVersion
type ApplicationVersionStatus struct {
	State    string       `json:"state,omitempty"`
	Message  string       `json:"message,omitempty"`
	UserName string       `json:"user_name,omitempty"`
	Updated  *metav1.Time `json:"updated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=appver
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="application name",type=string,JSONPath=`.spec.displayName.en`
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ApplicationVersion is the Schema for the applicationversions API
type ApplicationVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationVersionSpec   `json:"spec,omitempty"`
	Status ApplicationVersionStatus `json:"status,omitempty"`
}

// Maintainer describes a Chart maintainer.
type Maintainer struct {
	// Name is a user name or organization name
	Name string `json:"name,omitempty"`
	// Email is an optional email address to contact the named maintainer
	Email string `json:"email,omitempty"`
	// URL is an optional URL to an address for the named maintainer
	URL string `json:"url,omitempty"`
}

// Metadata for a Application detail.
type Metadata struct {
	DisplayName corev1alpha1.Locales `json:"displayName"`
	Version     string               `json:"version"`
	Home        string               `json:"home,omitempty"`
	Icon        string               `json:"icon,omitempty"`
	Description corev1alpha1.Locales `json:"description,omitempty"`
	Sources     []string             `json:"sources,omitempty"`
}

// +kubebuilder:object:root=true

// ApplicationVersionList contains a list of ApplicationVersion
type ApplicationVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationVersion `json:"items"`
}

func (in *ApplicationVersion) GetCreator() string {
	return getValue(in.Annotations, constants.CreatorAnnotationKey)
}

func (in *ApplicationVersion) GetWorkspace() string {
	return getValue(in.Labels, constants.WorkspaceLabelKey)
}

func (in *ApplicationVersion) GetAppID() string {
	return getValue(in.Labels, AppIDLabelKey)
}
