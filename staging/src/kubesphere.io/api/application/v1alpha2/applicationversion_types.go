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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubesphere.io/api/constants"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
)

// ApplicationVersionSpec defines the desired state of ApplicationVersion
type ApplicationVersionSpec struct {
	// metadata from chart
	*Metadata `json:",inline"`
	// appClassName
	AppClassName string `json:"appClassName"`
	// DataRef refers to a configMap which contains raw chart or yaml data.
	DataRef *ConfigMapKeyRef `json:"dataRef"`
	// create time
	Created *metav1.Time `json:"created,omitempty"`
	// Chart addition data
	ChartAdditionData *ChartAdditionData `json:"chartAdditionData,omitempty"`
}

type ConfigMapKeyRef struct {
	corev1.ConfigMapKeySelector `json:",inline"`
	Namespace                   string `json:"namespace"`
}

// ApplicationVersionStatus defines the observed state of ApplicationVersion
type ApplicationVersionStatus struct {
	State string  `json:"state,omitempty"`
	Audit []Audit `json:"audit,omitempty"`
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
	// A SemVer 2 conformant version string
	Version string `json:"version"`
	// The URL to a relevant project page, git repo, or contact person
	Home string `json:"home,omitempty"`
	// The URL to an icon file.
	Icon string `json:"icon,omitempty"`
	// A one-sentence description
	Description corev1alpha1.Locales `json:"description,omitempty"`
	// Source is the URL to the source code
	Sources []string `json:"sources,omitempty"`
	// A list of string keywords
	Keywords []string `json:"keywords,omitempty"`
	// A list of name and URL/email address combinations for the maintainer(s)
	Maintainers []*Maintainer `json:"maintainers,omitempty"`
}
type ChartAdditionData struct {
	ChartVersion string `json:"chartVersion,omitempty"`
	// chart url
	URLs []string `json:"urls,omitempty"`
	// chart digest
	Digest string `json:"digest,omitempty"`
	// Dependencies are a list of dependencies for a chart.
	Dependencies []*Dependency `json:"dependencies,omitempty"`
}

type Audit struct {
	// audit message
	Message string `json:"message,omitempty"`
	// audit state: submitted, passed, draft, active, rejected, suspended
	State string `json:"state,omitempty"`
	// audit time
	Time metav1.Time `json:"time"`
	// audit operator
	Operator     string `json:"operator,omitempty"`
	OperatorType string `json:"operatorType,omitempty"`
}

// Dependency describes a chart upon which another chart depends.
// Dependencies can be used to express developer intent, or to capture the state
// of a chart.
type Dependency struct {
	// Name is the name of the dependency.
	// This must mach the name in the dependency's Chart.yaml.
	Name string `json:"name"`
	// Version is the version (range) of this chart.
	// A lock file will always produce a single version, while a dependency
	// may contain a semantic version range.
	Version string `json:"version,omitempty"`
	// The URL to the repository.
	// Appending `index.yaml` to this string should result in a URL that can be
	// used to fetch the repository index.
	Repository string `json:"repository"`
	// A yaml path that resolves to a boolean, used for enabling/disabling charts (e.g. subchart1.enabled )
	Condition string `json:"condition,omitempty"`
	// Tags can be used to group charts for enabling/disabling together
	Tags []string `json:"tags,omitempty"`
	// Enabled bool determines if chart should be loaded
	Enabled bool `json:"enabled,omitempty"`
	// ImportValues holds the mapping of source values to parent key to be imported. Each item can be a
	// string or pair of child/parent sublist items.
	// ImportValues []interface{} `json:"import_values,omitempty"`

	// Alias usable alias to be used for the chart
	Alias string `json:"alias,omitempty"`
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

func (in *ApplicationVersion) GetHelmRepoID() string {
	return getValue(in.Labels, RepoIDLabelKey)
}

func (in *ApplicationVersion) State() string {
	if in.Status.State == "" {
		return StateDraft
	}

	return in.Status.State
}
