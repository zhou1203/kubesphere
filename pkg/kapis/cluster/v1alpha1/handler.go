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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/emicklei/go-restful/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/api"
	clusterv1alpha1 "kubesphere.io/kubesphere/pkg/api/cluster/v1alpha1"
	"kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
	"kubesphere.io/kubesphere/pkg/version"
)

const defaultTimeout = 10 * time.Second

type handler struct {
	client runtimeclient.Client
}

func newHandler(cacheClient runtimeclient.Client) *handler {
	return &handler{
		client: cacheClient,
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

	cluster := &v1alpha1.Cluster{}
	if err := h.client.Get(context.Background(), types.NamespacedName{Name: clusterName}, cluster); err != nil {
		api.HandleBadRequest(response, request, err)
		return
	}
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

	if _, err = validateKubeSphereAPIServer(clientSet); err != nil {
		api.HandleBadRequest(response, request, fmt.Errorf("unable validate kubesphere endpoint, %v", err))
		return
	}

	if err = h.validateMemberClusterConfiguration(clientSet); err != nil {
		api.HandleBadRequest(response, request, fmt.Errorf("failed to validate member cluster configuration, err: %v", err))
		return
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

	cluster = cluster.DeepCopy()
	cluster.Spec.Connection.KubeConfig = req.KubeConfig
	if err = h.client.Update(context.TODO(), cluster); err != nil {
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

	clusterList := &v1alpha1.ClusterList{}
	if err := h.client.List(context.Background(), clusterList); err != nil {
		return err
	}

	// clusters with the exactly same kube-system namespace UID considered to be one
	// MUST not import the same cluster twice
	for _, existedCluster := range clusterList.Items {
		if existedCluster.Status.UID == kubeSystem.UID {
			return fmt.Errorf("cluster %s already exists (%s), MUST not import the same cluster twice", clusterName, existedCluster.Name)
		}
	}

	_, err = clientSet.Discovery().ServerVersion()
	return err
}

// validateKubeSphereAPIServer uses version api to check the accessibility
func validateKubeSphereAPIServer(clusterClient kubernetes.Interface) (*version.Info, error) {
	response, err := clusterClient.CoreV1().Services(constants.KubeSphereNamespace).
		ProxyGet("http", constants.KubeSphereAPIServerName, "80", "/version", nil).
		DoRaw(context.Background())
	if err != nil {
		return nil, fmt.Errorf("invalid response: %s, please make sure %s.%s.svc of member cluster is up and running", response, constants.KubeSphereAPIServerName, constants.KubeSphereNamespace)
	}

	ver := version.Info{}
	if err = json.Unmarshal(response, &ver); err != nil {
		return nil, fmt.Errorf("invalid response: %s, please make sure %s.%s.svc of member cluster is up and running", response, constants.KubeSphereAPIServerName, constants.KubeSphereNamespace)
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
	hostCm := &v1.ConfigMap{}

	if err := h.client.Get(context.Background(),
		types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: constants.KubeSphereConfigName}, hostCm); err != nil {
		return nil, fmt.Errorf("failed to get host cluster %s/configmap/%s, err: %s",
			constants.KubeSphereNamespace, constants.KubeSphereConfigName, err)
	}

	return config.GetFromConfigMap(hostCm)
}
