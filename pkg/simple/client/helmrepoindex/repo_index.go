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

package helmrepoindex

import (
	"fmt"
	"strings"

	helmrepo "helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"

	"kubesphere.io/api/application/v1alpha2"
)

const IndexYaml = "index.yaml"

func LoadRepoIndex(u string, cred *v1alpha2.HelmRepoCredential) (*helmrepo.IndexFile, error) {
	if !strings.HasSuffix(u, "/") {
		u = fmt.Sprintf("%s/%s", u, IndexYaml)
	} else {
		u = fmt.Sprintf("%s%s", u, IndexYaml)
	}

	resp, err := LoadChart(u, cred)
	if err != nil {
		return nil, err
	}

	indexFile, err := loadIndex(resp.Bytes())
	if err != nil {
		return nil, err
	}

	return indexFile, nil
}

// loadIndex loads an index file and does minimal validity checking.
// This will fail if API Version is not set (ErrNoAPIVersion) or if the unmarshal fails.
func loadIndex(data []byte) (*helmrepo.IndexFile, error) {
	i := &helmrepo.IndexFile{}
	if err := yaml.Unmarshal(data, i); err != nil {
		return i, err
	}
	i.SortEntries()
	if i.APIVersion == "" {
		return i, helmrepo.ErrNoAPIVersion
	}
	return i, nil
}
