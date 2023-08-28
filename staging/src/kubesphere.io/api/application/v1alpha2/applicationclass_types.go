/*
Copyright 2023 The KubeSphere Authors.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationClassSpec defines the desired state of ApplicationClass
type ApplicationClassSpec struct {
	// Specify the installer, e.g. ks-yaml,ks-helm
	Installer string `json:"installer"`
	// extensible parameters, provided for the installer
	Parameters map[string]string `json:"parameters,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=appcls
// +kubebuilder:printcolumn:name="Installer",type=string,JSONPath=".spec.installer"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ApplicationClass is the Schema for the applications API
type ApplicationClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ApplicationClassSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ApplicationClassList contains a list of ApplicationClass
type ApplicationClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationClass `json:"items"`
}
