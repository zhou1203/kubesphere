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

package v1alpha1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/emicklei/go-restful"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"

	"kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/api"
	clusterv1alpha1 "kubesphere.io/kubesphere/pkg/api/cluster/v1alpha1"
	"kubesphere.io/kubesphere/pkg/apiserver/config"
	kubesphere "kubesphere.io/kubesphere/pkg/client/clientset/versioned"
	"kubesphere.io/kubesphere/pkg/client/informers/externalversions"
	clusterlister "kubesphere.io/kubesphere/pkg/client/listers/cluster/v1alpha1"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
	"kubesphere.io/kubesphere/pkg/version"
)

const (
	defaultTimeout      = 10 * time.Second
	KubeSphereApiServer = "ks-apiserver"
)

type handler struct {
	ksclient        kubesphere.Interface
	clusterLister   clusterlister.ClusterLister
	configMapLister v1.ConfigMapLister
}

func newHandler(ksclient kubesphere.Interface, k8sInformers k8sinformers.SharedInformerFactory, ksInformers externalversions.SharedInformerFactory) *handler {
	return &handler{
		ksclient:        ksclient,
		clusterLister:   ksInformers.Cluster().V1alpha1().Clusters().Lister(),
		configMapLister: k8sInformers.Core().V1().ConfigMaps().Lister(),
	}
}

// updateKubeConfig updates the kubeconfig of the specific cluster, this API is used to update expired kubeconfig.
func (h *handler) updateKubeConfig(request *restful.Request, response *restful.Response) {
	var req clusterv1alpha1.UpdateClusterRequest
	if err := request.ReadEntity(&req); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	clusterName := request.PathParameter("cluster")
	obj, err := h.clusterLister.Get(clusterName)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	cluster := obj.DeepCopy()
	if _, ok := cluster.Labels[v1alpha1.HostCluster]; ok {
		api.HandleBadRequest(response, request, fmt.Errorf("update kubeconfig of the host cluster is not allowed"))
		return
	}
	// For member clusters that use proxy mode, we don't need to update the kubeconfig,
	// if the certs expired, just restart the tower component in the host cluster, it will renew the cert.
	if cluster.Spec.Connection.Type == v1alpha1.ConnectionTypeProxy {
		api.HandleBadRequest(response, request, fmt.Errorf(
			"update kubeconfig of member clusters which using proxy mode is not allowed, their certs are managed and will be renewed by tower",
		))
		return
	}

	if len(req.KubeConfig) == 0 {
		api.HandleBadRequest(response, request, fmt.Errorf("cluster kubeconfig MUST NOT be empty"))
		return
	}
	config, err := k8sutil.LoadKubeConfigFromBytes(req.KubeConfig)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	config.Timeout = defaultTimeout
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	if _, err = clientSet.Discovery().ServerVersion(); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	_, err = validateKubeSphereAPIServer(config)
	if err != nil {
		api.HandleBadRequest(response, request, fmt.Errorf("unable validate kubesphere endpoint, %v", err))
		return
	}

	err = h.validateMemberClusterConfiguration(clientSet)
	if err != nil {
		api.HandleBadRequest(response, request, fmt.Errorf("failed to validate member cluster configuration, err: %v", err))
	}

	// Check if the cluster is the same
	kubeSystem, err := clientSet.CoreV1().Namespaces().Get(context.TODO(), metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	if kubeSystem.UID != cluster.Status.UID {
		api.HandleBadRequest(
			response, request, fmt.Errorf(
				"this kubeconfig corresponds to a different cluster than the previous one, you need to make sure that kubeconfig is not from another cluster",
			))
		return
	}

	cluster.Spec.Connection.KubeConfig = req.KubeConfig
	if _, err = h.ksclient.ClusterV1alpha1().Clusters().Update(context.TODO(), cluster, metav1.UpdateOptions{}); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	response.WriteHeader(http.StatusOK)
}

// ValidateCluster validate cluster kubeconfig and kubesphere apiserver address, check their accessibility
func (h *handler) validateCluster(request *restful.Request, response *restful.Response) {
	var cluster v1alpha1.Cluster

	err := request.ReadEntity(&cluster)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if cluster.Spec.Connection.Type != v1alpha1.ConnectionTypeDirect {
		api.HandleBadRequest(response, request, fmt.Errorf("cluster connection type MUST be direct"))
		return
	}

	if len(cluster.Spec.Connection.KubeConfig) == 0 {
		api.HandleBadRequest(response, request, fmt.Errorf("cluster kubeconfig MUST NOT be empty"))
		return
	}

	config, err := k8sutil.LoadKubeConfigFromBytes(cluster.Spec.Connection.KubeConfig)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
	config.Timeout = defaultTimeout
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	if err = h.validateKubeConfig(cluster.Name, clientSet); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}

	response.WriteHeader(http.StatusOK)
}

// validateKubeConfig takes base64 encoded kubeconfig and check its validity
func (h *handler) validateKubeConfig(clusterName string, clientSet kubernetes.Interface) error {
	kubeSystem, err := clientSet.CoreV1().Namespaces().Get(context.TODO(), metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		return err
	}

	clusters, err := h.clusterLister.List(labels.Everything())
	if err != nil {
		return err
	}

	// clusters with the exactly same kube-system namespace UID considered to be one
	// MUST not import the same cluster twice
	for _, existedCluster := range clusters {
		if existedCluster.Status.UID == kubeSystem.UID {
			return fmt.Errorf("cluster %s already exists (%s), MUST not import the same cluster twice", clusterName, existedCluster.Name)
		}
	}

	_, err = clientSet.Discovery().ServerVersion()
	return err
}

// validateKubeSphereAPIServer uses version api to check the accessibility
func validateKubeSphereAPIServer(config *rest.Config) (*version.Info, error) {
	transport, err := rest.TransportFor(config)
	if err != nil {
		return nil, err
	}
	client := http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}

	response, err := client.Get(fmt.Sprintf("%s/api/v1/namespaces/%s/services/:%s:/proxy/kapis/version", config.Host, constants.KubeSphereNamespace, KubeSphereApiServer))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	responseBytes, _ := io.ReadAll(response.Body)
	responseBody := string(responseBytes)

	response.Body = io.NopCloser(bytes.NewBuffer(responseBytes))

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid response: %s , please make sure %s.%s.svc of member cluster is up and running", KubeSphereApiServer, constants.KubeSphereNamespace, responseBody)
	}

	ver := version.Info{}
	err = json.NewDecoder(response.Body).Decode(&ver)
	if err != nil {
		return nil, fmt.Errorf("invalid response: %s , please make sure %s.%s.svc of member cluster is up and running", KubeSphereApiServer, constants.KubeSphereNamespace, responseBody)
	}

	return &ver, nil
}

// validateMemberClusterConfiguration compares host and member cluster jwt, if they are not same, it changes member
// cluster jwt to host's, then restart member cluster ks-apiserver.
func (h *handler) validateMemberClusterConfiguration(clientSet kubernetes.Interface) error {
	hConfig, err := h.getHostClusterConfig()
	if err != nil {
		return err
	}

	mConfig, err := h.getMemberClusterConfig(clientSet)
	if err != nil {
		return err
	}

	if hConfig.AuthenticationOptions.JwtSecret != mConfig.AuthenticationOptions.JwtSecret {
		return fmt.Errorf("hostcluster Jwt is not equal to member cluster jwt, please edit the member cluster cluster config")
	}

	return nil
}

// getMemberClusterConfig returns KubeSphere running config by the given member cluster kubeconfig
func (h *handler) getMemberClusterConfig(clientSet kubernetes.Interface) (*config.Config, error) {
	memberCm, err := clientSet.CoreV1().ConfigMaps(constants.KubeSphereNamespace).Get(context.Background(), constants.KubeSphereConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return config.GetFromConfigMap(memberCm)
}

// getHostClusterConfig returns KubeSphere running config from host cluster ConfigMap
func (h *handler) getHostClusterConfig() (*config.Config, error) {
	hostCm, err := h.configMapLister.ConfigMaps(constants.KubeSphereNamespace).Get(constants.KubeSphereConfigName)
	if err != nil {
		return nil, fmt.Errorf("failed to get host cluster %s/configmap/%s, err: %s", constants.KubeSphereNamespace, constants.KubeSphereConfigName, err)
	}

	return config.GetFromConfigMap(hostCm)
}
