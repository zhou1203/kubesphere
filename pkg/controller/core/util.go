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
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/version"
)

func generatePodName(repoName string) string {
	return fmt.Sprintf("%s-%s", "catalog", repoName)
}

func getRecommendedExtensionVersion(versions []corev1alpha1.ExtensionVersion, k8sVersion string) (string, error) {
	if len(versions) == 0 {
		return "", nil
	}

	kubeVersion, err := semver.NewVersion(k8sVersion)
	if err != nil {
		return "", fmt.Errorf("parse Kubernetes version failed: %v", err)
	}
	ksVersion, err := semver.NewVersion(version.Get().GitVersion)
	if err != nil {
		return "", fmt.Errorf("parse KubeSphere version failed: %v", err)
	}

	var matchedVersions []*semver.Version

	for _, v := range versions {
		targetKubeVersion, err := semver.NewVersion(v.Spec.KubeVersion)
		if err != nil {
			// If the semver is invalid, just ignore it.
			klog.V(2).Infof("parse version failed, extension version: %s, err: %s", v.Spec.KubeVersion, err)
			continue
		}
		targetKSVersion, err := semver.NewVersion(v.Spec.KSVersion)
		if err != nil {
			klog.V(2).Infof("parse version failed, extension version: %s, err: %s", v.Spec.KSVersion, err)
			continue
		}
		if kubeVersion.Compare(targetKubeVersion) >= 0 && ksVersion.Compare(targetKSVersion) >= 0 {
			targetVersion, err := semver.NewVersion(v.Spec.Version)
			if err != nil {
				klog.V(2).Infof("parse version failed, extension version: %s, err: %s", v.Spec.Version, err)
				continue
			}
			matchedVersions = append(matchedVersions, targetVersion)
		}
	}

	if len(matchedVersions) == 0 {
		return "", nil
	}

	sort.Slice(matchedVersions, func(i, j int) bool {
		return matchedVersions[i].Compare(matchedVersions[j]) >= 0
	})
	return matchedVersions[0].Original(), nil
}

func getLatestExtensionVersion(versions []corev1alpha1.ExtensionVersion) *corev1alpha1.ExtensionVersion {
	if len(versions) == 0 {
		return nil
	}

	var latestVersion *corev1alpha1.ExtensionVersion
	var latestSemver *semver.Version

	for i := range versions {
		currSemver, err := semver.NewVersion(versions[i].Spec.Version)
		if err == nil {
			if latestSemver == nil {
				// the first valid semver
				latestSemver = currSemver
				latestVersion = &versions[i]
			} else if latestSemver.LessThan(currSemver) {
				// find a newer valid semver
				latestSemver = currSemver
				latestVersion = &versions[i]
			}
		} else {
			// If the semver is invalid, just ignore it.
			klog.V(2).Infof("parse version failed, extension version: %s, err: %s", versions[i].Name, err)
		}
	}
	return latestVersion
}

func ContainsAnnotation(obj metav1.Object, key string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	if _, ok := annotations[key]; ok {
		return true
	}
	return false
}
