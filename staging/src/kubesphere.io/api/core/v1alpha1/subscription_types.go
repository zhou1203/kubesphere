/*
Copyright 2022 The KubeSphere Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtensionRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SubscriptionSpec struct {
	Extension ExtensionRef `json:"extension"`
	Enabled   bool         `json:"enabled"`
	Config    string       `json:"config,omitempty"`
}

type SubscriptionStatus struct {
	State           string             `json:"state,omitempty"`
	ReleaseName     string             `json:"releaseName,omitempty"`
	TargetNamespace string             `json:"targetNamespace,omitempty"`
	JobName         string             `json:"jobName,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories="extensions",scope="Cluster"

// Subscription describes the configuration and the extension version to be subscribed.
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SubscriptionSpec   `json:"spec,omitempty"`
	Status            SubscriptionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subscription `json:"items"`
}