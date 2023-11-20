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

package v2

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	appv2 "kubesphere.io/api/application/v2"

	"kubesphere.io/kubesphere/pkg/server/params"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

func appReviewsStatusFilter(versions []appv2.ApplicationVersion, conditions *params.Conditions) []runtime.Object {
	filtered := make([]runtime.Object, 0)
	for _, version := range versions {
		curVersion := version
		if conditions.Match[Status] != "" {
			states := strings.Split(conditions.Match[Status], "|")
			state := curVersion.Status.State
			if !sliceutil.HasString(states, state) {
				continue
			}
		}
		filtered = append(filtered, &curVersion)
	}

	return filtered
}

func appStatusFilter(apps []appv2.Application, conditions *params.Conditions) []runtime.Object {
	filtered := make([]runtime.Object, 0)
	for _, app := range apps {
		curApp := app
		if conditions.Match[Status] != "" {
			states := strings.Split(conditions.Match[Status], "|")
			state := curApp.Status.State
			if !sliceutil.HasString(states, state) {
				continue
			}
		}
		filtered = append(filtered, &curApp)
	}

	return filtered
}
