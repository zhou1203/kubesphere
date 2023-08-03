/*
Copyright 2022 KubeSphere Authors

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

package core

import (
	"testing"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/version"
)

func TestGetRecommendedExtensionVersion(t *testing.T) {
	tests := []struct {
		name       string
		versions   []corev1alpha1.ExtensionVersion
		k8sVersion string
		ksVersion  string
		wanted     string
	}{
		{
			name: "normal test",
			versions: []corev1alpha1.ExtensionVersion{
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // match
						Version:     "1.0.0",
						KubeVersion: ">=1.19.0",
						KSVersion:   ">=4.0.0",
					},
				},
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // match
						Version:     "1.1.0",
						KubeVersion: ">=1.20.0",
						KSVersion:   ">=4.0.0",
					},
				},
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // KubeVersion not match
						Version:     "1.2.0",
						KubeVersion: ">=1.21.0",
						KSVersion:   ">=4.0.0",
					},
				},
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // KSVersion not match
						Version:     "1.3.0",
						KubeVersion: ">=1.20.0",
						KSVersion:   ">=4.1.0",
					},
				},
			},
			k8sVersion: "1.20.0",
			ksVersion:  "4.0.0",
			wanted:     "1.1.0",
		},
		{
			name: "no matches test",
			versions: []corev1alpha1.ExtensionVersion{
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // KubeVersion not match
						Version:     "1.2.0",
						KubeVersion: ">=1.21.0",
						KSVersion:   ">=4.0.0",
					},
				},
				{
					Spec: corev1alpha1.ExtensionVersionSpec{ // KSVersion not match
						Version:     "1.3.0",
						KubeVersion: ">=1.20.0",
						KSVersion:   ">=4.1.0",
					},
				},
			},
			k8sVersion: "1.20.0",
			ksVersion:  "4.0.0",
			wanted:     "",
		},
		{
			name: "match 1.3.0",
			versions: []corev1alpha1.ExtensionVersion{
				{
					Spec: corev1alpha1.ExtensionVersionSpec{
						Version:     "1.2.0",
						KubeVersion: ">=1.19.0",
						KSVersion:   ">=3.0.0",
					},
				},
				{
					Spec: corev1alpha1.ExtensionVersionSpec{
						Version:     "1.3.0",
						KubeVersion: ">=1.19.0",
						KSVersion:   ">=4.0.0-alpha",
					},
				},
			},
			k8sVersion: "1.25.4",
			ksVersion:  "4.0.0-beta.5+ae34",
			wanted:     "1.3.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version.SetGitVersion(tt.ksVersion)
			if got, _ := getRecommendedExtensionVersion(tt.versions, tt.k8sVersion); got != tt.wanted {
				t.Errorf("getRecommendedExtensionVersion() = %v, want %v", got, tt.wanted)
			}
		})
	}
}
