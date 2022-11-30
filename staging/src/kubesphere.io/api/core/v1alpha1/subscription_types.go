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

type Placement struct {
	Clusters        []Cluster             `json:"clusters,omitempty"`
	ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`
}

type Cluster struct {
	Name string `json:"name"`
}

type Override struct {
	ClusterName    string `json:"clusterName"`
	ConfigOverride string `json:"configOverride"`
}

type Delivery struct {
	Placement *Placement `json:"placement,omitempty"`
	Overrides []Override `json:"overrides,omitempty"`
}

type DeliveryStatus struct {
	State           string `json:"state,omitempty"`
	ClusterName     string `json:"clusterName,omitempty"`
	ReleaseName     string `json:"releaseName,omitempty"`
	TargetNamespace string `json:"targetNamespace,omitempty"`
	JobName         string `json:"jobName,omitempty"`
}

type ExtensionRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SubscriptionSpec struct {
	Extension ExtensionRef `json:"extension"`
	Enabled   bool         `json:"enabled"`
	Config    string       `json:"config,omitempty"`
	Delivery  *Delivery    `json:"delivery,omitempty"`
}

type SubscriptionStatus struct {
	// Describe the installation status of the extension
	State           string `json:"state,omitempty"`
	ReleaseName     string `json:"releaseName,omitempty"`
	TargetNamespace string `json:"targetNamespace,omitempty"`
	JobName         string `json:"jobName,omitempty"`
	// Describe the subchart installation status of the extension
	DeliveryStatuses []DeliveryStatus   `json:"deliveryStatuses"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
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
