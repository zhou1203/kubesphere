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
	"bytes"
	"net/url"
	"time"

	"helm.sh/helm/v3/pkg/getter"

	"kubesphere.io/api/application/v1alpha2"
)

func loadData(u string, cred *v1alpha2.HelmRepoCredential) (*bytes.Buffer, error) {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	var resp *bytes.Buffer

	skipTLS := true
	if cred.InsecureSkipTLSVerify != nil && !*cred.InsecureSkipTLSVerify {
		skipTLS = false
	}

	indexURL := parsedURL.String()
	// TODO add user-agent
	g, _ := getter.NewHTTPGetter()
	resp, err = g.Get(indexURL,
		getter.WithTimeout(5*time.Minute),
		getter.WithURL(u),
		getter.WithInsecureSkipVerifyTLS(skipTLS),
		getter.WithTLSClientConfig(cred.CertFile, cred.KeyFile, cred.CAFile),
		getter.WithBasicAuth(cred.Username, cred.Password),
	)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func LoadChart(u string, cred *v1alpha2.HelmRepoCredential) (*bytes.Buffer, error) {
	return loadData(u, cred)
}
