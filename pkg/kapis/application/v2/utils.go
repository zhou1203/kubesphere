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
	"bytes"
	"errors"

	"helm.sh/helm/v3/pkg/chart/loader"
	"k8s.io/client-go/dynamic"
	appv2 "kubesphere.io/api/application/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/simple/client/application"
)

const (
	Status = "status"
)

func parseRequest(createRequest application.AppRequest, workspace string) (appRequest, vRequest application.AppRequest, err error) {

	appRequest.Description = createRequest.Description
	appRequest.AliasName = createRequest.AliasName
	appRequest.Icon = createRequest.Icon
	appRequest.CategoryName = createRequest.CategoryName

	if createRequest.AppType == appv2.AppTypeHelm {
		if createRequest.Package == nil || len(createRequest.Package) == 0 {
			return appRequest, vRequest, errors.New("package is empty")
		}
		chartPack, err := loader.LoadArchive(bytes.NewReader(createRequest.Package))
		if err != nil {
			return appRequest, vRequest, err
		}
		appRequest, vRequest = helmRequest(chartPack, workspace, createRequest.Package)
		return appRequest, vRequest, nil
	}
	if createRequest.AppType == appv2.AppTypeYaml || createRequest.AppType == appv2.AppTypeEdge {
		createRequest.Workspace = workspace
		createRequest.RepoName = "configyaml"
		return createRequest, createRequest, nil
	}

	return appRequest, vRequest, errors.New("not support app type")
}

func (h *appHandler) getCluster(app *appv2.ApplicationRelease) (runtimeclient.Client, *dynamic.DynamicClient, *clusterv1alpha1.Cluster, error) {
	clusterName := app.GetRlsCluster()
	runtimeClient, err := h.clusterClient.GetRuntimeClient(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}
	clusterClient, err := h.clusterClient.GetClusterClient(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(clusterClient.RestConfig)
	if err != nil {
		return nil, nil, nil, err
	}
	cluster, err := h.clusterClient.Get(clusterName)
	if err != nil {
		return nil, nil, nil, err
	}
	return runtimeClient, dynamicClient, cluster, nil
}
