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
	"crypto/md5"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubesphere.io/api/constants"
)

type HelmRepoCredential struct {
	// chart repository username
	Username string `json:"username,omitempty"`
	// chart repository password
	Password string `json:"password,omitempty"`
	// identify HTTPS client using this SSL certificate file
	CertFile string `json:"certFile,omitempty"`
	// identify HTTPS client using this SSL key file
	KeyFile string `json:"keyFile,omitempty"`
	// verify certificates of HTTPS-enabled servers using this CA bundle
	CAFile string `json:"caFile,omitempty"`
	// skip tls certificate checks for the repository, default is ture
	InsecureSkipTLSVerify *bool `json:"insecureSkipTLSVerify,omitempty"`
}

// HelmRepoSpec defines the desired state of HelmRepo
type HelmRepoSpec struct {
	// name of the repo
	Name string `json:"name"`
	// helm repo url
	Url string `json:"url"`
	// helm repo credential
	Credential HelmRepoCredential `json:"credential,omitempty"`
	// chart repo description from frontend
	Description string `json:"description,omitempty"`
	// sync period in seconds, no sync when SyncPeriod=0, the minimum SyncPeriod is 180s
	SyncPeriod int `json:"syncPeriod,omitempty"`
}

// HelmRepoStatus defines the observed state of HelmRepo
type HelmRepoStatus struct {
	// status last update time
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
	// current state of the repo, successful, failed or syncing
	State string `json:"state,omitempty"`
	// current release spec hash
	// This is used to compare whether the spec has been modified to determine sync is needed.
	SpecHash string `json:"specHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,path=helmrepos,shortName=hrepo
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workspace",type="string",JSONPath=".metadata.labels.kubesphere\\.io/workspace"
// +kubebuilder:printcolumn:name="url",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HelmRepo is the Schema for the helmrepoes API
type HelmRepo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HelmRepoSpec   `json:"spec,omitempty"`
	Status HelmRepoStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HelmRepoList contains a list of HelmRepo
type HelmRepoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HelmRepo `json:"items"`
}

func (in *HelmRepo) GetWorkspace() string {
	return getValue(in.Labels, constants.WorkspaceLabelKey)
}

func (in *HelmRepo) GetCreator() string {
	return getValue(in.Annotations, constants.CreatorAnnotationKey)
}

func (in *HelmRepo) HashSpec() string {
	specJSON, _ := json.Marshal(in.Spec)
	return fmt.Sprintf("%x", md5.Sum(specJSON))
}
