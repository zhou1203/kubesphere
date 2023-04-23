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
	"bytes"
	"encoding/hex"
	goerrors "errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/storage/driver"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"

	"kubesphere.io/kubesphere/pkg/version"
)

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
		var kubeVersionMatched, ksVersionMatched bool

		if v.Spec.KubeVersion == "" {
			kubeVersionMatched = true
		} else {
			targetKubeVersion, err := semver.NewConstraint(v.Spec.KubeVersion)
			if err != nil {
				// If the semver is invalid, just ignore it.
				klog.Warningf("failed to parse Kubernetes version constraints: kubeVersion: %s, err: %s", v.Spec.KubeVersion, err)
				continue
			}
			kubeVersionMatched = targetKubeVersion.Check(kubeVersion)
		}

		if v.Spec.KSVersion == "" {
			ksVersionMatched = true
		} else {
			targetKSVersion, err := semver.NewConstraint(v.Spec.KSVersion)
			if err != nil {
				klog.Warningf("failed to parse KubeSphere version constraints: ksVersion: %s, err: %s", v.Spec.KSVersion, err)
				continue
			}
			ksVersionMatched = targetKSVersion.Check(ksVersion)
		}

		if kubeVersionMatched && ksVersionMatched {
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
			klog.Warningf("parse version failed, extension version: %s, err: %s", versions[i].Name, err)
		}
	}
	return latestVersion
}

func isReleaseNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), driver.ErrReleaseNotFound.Error())
}

func clusterConfig(sub *corev1alpha1.Subscription, clusterName string) string {
	for cluster, config := range sub.Spec.ClusterScheduling.Overrides {
		if cluster == clusterName {
			return config
		}
	}
	return sub.Spec.Config
}

func usesPermissions(chartData []byte) (rbacv1.ClusterRole, rbacv1.Role) {
	var clusterRole rbacv1.ClusterRole
	var role rbacv1.Role

	files, err := loader.LoadArchiveFiles(bytes.NewReader(chartData))
	if err != nil {
		return clusterRole, role
	}
	for _, file := range files {
		if file.Name == permissionDefinitionFile {
			// decoder := yaml.NewDecoder(bytes.NewReader(file.Data))
			decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(file.Data), 1024)
			for {
				result := new(rbacv1.Role)
				// create new spec here
				// pass a reference to spec reference
				err := decoder.Decode(&result)
				// check it was parsed
				if result == nil {
					continue
				}
				// break the loop in case of EOF
				if goerrors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return clusterRole, role
				}
				if result.Kind == "ClusterRole" {
					clusterRole.Rules = append(clusterRole.Rules, result.Rules...)
				}
				if result.Kind == "Role" {
					role.Rules = append(clusterRole.Rules, result.Rules...)
				}
			}
		}
	}
	return clusterRole, role
}

func hasCluster(clusters []clusterv1alpha1.Cluster, clusterName string) bool {
	for _, cluster := range clusters {
		if cluster.Name == clusterName {
			return true
		}
	}
	return false
}

func isServerSideError(err error) bool {
	return errors.IsTimeout(err) ||
		errors.IsServerTimeout(err) ||
		errors.IsServiceUnavailable(err) ||
		errors.IsInternalError(err) ||
		errors.IsUnexpectedServerError(err) ||
		os.IsTimeout(err)
}

func fnvString(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

func configChanged(sub *corev1alpha1.Subscription) bool {
	return fnvString(sub.Spec.Config) != sub.Annotations[corev1alpha1.SubscriptionConfigHashAnnotation]
}

func setConfigHash(sub *corev1alpha1.Subscription, config string) {
	configHash := fnvString(config)
	if sub.Annotations == nil {
		sub.Annotations = map[string]string{
			corev1alpha1.SubscriptionConfigHashAnnotation: configHash,
		}
	} else {
		sub.Annotations[corev1alpha1.SubscriptionConfigHashAnnotation] = configHash
	}
}
