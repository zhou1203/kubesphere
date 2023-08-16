/*
Copyright 2020 KubeSphere Authors

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

package cluster

import (
	"context"
	"os"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	"kubesphere.io/utils/helm"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/constants"
)

const releaseName = "ks-core"

func installKSCoreInMemberCluster(kubeConfig, jwtSecret string) error {
	helmConf, err := helm.InitHelmConf(kubeConfig, constants.KubeSphereNamespace)
	if err != nil {
		return err
	}

	chart, err := loader.Load("/var/helm-charts/ks-core") // in-container chart path
	if err != nil {
		return err
	}

	// values example:
	// 	map[string]interface{}{
	//		"nestedKey": map[string]interface{}{
	//			"simpleKey": "simpleValue",
	//		},
	//  }
	values := map[string]interface{}{
		"role": "member",
		"config": map[string]interface{}{
			"jwtSecret": jwtSecret,
		},
	}

	helmStatus := action.NewStatus(helmConf)
	if _, err = helmStatus.Run(releaseName); err != nil { // the release not exists
		install := action.NewInstall(helmConf)
		install.Namespace = constants.KubeSphereNamespace
		install.CreateNamespace = true
		install.Wait = true
		install.ReleaseName = releaseName
		install.Timeout = time.Minute * 5
		if _, err = install.Run(chart, values); err != nil {
			return err
		}
		return nil
	}

	upgrade := action.NewUpgrade(helmConf)
	upgrade.Namespace = constants.KubeSphereNamespace
	upgrade.Install = true
	upgrade.Wait = true
	upgrade.Timeout = time.Minute * 5
	if _, err = upgrade.Run(releaseName, chart, values); err != nil {
		return err
	}
	return nil
}

func getKubeSphereConfig(ctx context.Context, client runtimeclient.Client) (*config.Config, error) {
	cm := &corev1.ConfigMap{}
	if err := client.Get(ctx, types.NamespacedName{Name: constants.KubeSphereConfigName, Namespace: constants.KubeSphereNamespace}, cm); err != nil {
		return nil, err
	}
	configData, err := config.GetFromConfigMap(cm)
	if err != nil {
		return nil, err
	}
	return configData, nil
}

func hasCondition(conditions []clusterv1alpha1.ClusterCondition, conditionsType clusterv1alpha1.ClusterConditionType) bool {
	for _, condition := range conditions {
		if condition.Type == conditionsType && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func buildKubeConfigFromRestConfig(config *rest.Config) ([]byte, error) {
	apiConfig := api.NewConfig()

	apiCluster := &api.Cluster{
		Server:                   config.Host,
		CertificateAuthorityData: config.CAData,
	}

	// generated kubeconfig will be used by cluster federation, CAFile is not
	// accepted by kubefed, so we need read CAFile
	if len(apiCluster.CertificateAuthorityData) == 0 && len(config.CAFile) != 0 {
		caData, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}

		apiCluster.CertificateAuthorityData = caData
	}

	apiConfig.Clusters["kubernetes"] = apiCluster

	apiConfig.AuthInfos["kubernetes-admin"] = &api.AuthInfo{
		ClientCertificateData: config.CertData,
		ClientKeyData:         config.KeyData,
		Token:                 config.BearerToken,
		TokenFile:             config.BearerTokenFile,
		Username:              config.Username,
		Password:              config.Password,
	}

	apiConfig.Contexts["kubernetes-admin@kubernetes"] = &api.Context{
		Cluster:  "kubernetes",
		AuthInfo: "kubernetes-admin",
	}

	apiConfig.CurrentContext = "kubernetes-admin@kubernetes"

	return clientcmd.Write(*apiConfig)
}
