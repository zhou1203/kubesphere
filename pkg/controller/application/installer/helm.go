package installer

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/getter"
	helmrepo "helm.sh/helm/v3/pkg/repo"
	appv2 "kubesphere.io/api/application/v2"
	"sigs.k8s.io/yaml"
)

func HelmPull(u string, cred appv2.HelmRepoCredential) (*bytes.Buffer, error) {
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
	g, _ := getter.NewHTTPGetter()
	resp, err = g.Get(indexURL,
		getter.WithTimeout(5*time.Minute),
		getter.WithURL(u),
		getter.WithInsecureSkipVerifyTLS(skipTLS),
		getter.WithTLSClientConfig(cred.CertFile, cred.KeyFile, cred.CAFile),
		getter.WithBasicAuth(cred.Username, cred.Password),
	)

	return resp, err
}

func LoadRepoIndex(u string, cred appv2.HelmRepoCredential) (idx helmrepo.IndexFile, err error) {
	if !strings.HasSuffix(u, "/") {
		u = fmt.Sprintf("%s/index.yaml", u)
	} else {
		u = fmt.Sprintf("%sindex.yaml", u)
	}

	resp, err := HelmPull(u, cred)
	if err != nil {
		return idx, err
	}
	if err = yaml.Unmarshal(resp.Bytes(), &idx); err != nil {
		return idx, err
	}
	idx.SortEntries()

	return idx, nil
}
