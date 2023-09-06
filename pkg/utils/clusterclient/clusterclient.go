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

package clusterclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/scheme"
)

type Interface interface {
	Get(string) (*clusterv1alpha1.Cluster, error)
	ListClusters(ctx context.Context) ([]clusterv1alpha1.Cluster, error)
	GetClusterClient(string) (*ClusterClient, error)
	GetRuntimeClient(string) (runtimeclient.Client, error)
}

type ClusterClient struct {
	KubernetesURL     *url.URL
	KubeSphereURL     *url.URL
	KubernetesVersion string
	RestConfig        *rest.Config
	Transport         http.RoundTripper
	Client            runtimeclient.Client
	KubernetesClient  kubernetes.Interface
}

type clusterClients struct {
	sync.RWMutex
	// build an in memory cluster cache to speed things up
	clients map[string]*ClusterClient
	cache   runtimecache.Cache
}

func NewClusterClientSet(runtimeCache runtimecache.Cache) (Interface, error) {
	c := &clusterClients{
		clients: make(map[string]*ClusterClient),
		cache:   runtimeCache,
	}

	clusterInformer, err := runtimeCache.GetInformerForKind(context.Background(), clusterv1alpha1.SchemeGroupVersion.WithKind(clusterv1alpha1.ResourceKindCluster))
	if err != nil {
		return nil, err
	}

	if _, err = clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if _, err = c.addCluster(obj); err != nil {
				klog.Error(err)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCluster := oldObj.(*clusterv1alpha1.Cluster)
			newCluster := newObj.(*clusterv1alpha1.Cluster)
			if !reflect.DeepEqual(oldCluster.Spec, newCluster.Spec) {
				c.removeCluster(oldCluster)
				if _, err = c.addCluster(newObj); err != nil {
					klog.Error(err)
				}
			}
		},
		DeleteFunc: c.removeCluster,
	}); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *clusterClients) addCluster(obj interface{}) (*ClusterClient, error) {
	cluster := obj.(*clusterv1alpha1.Cluster)
	klog.V(4).Infof("add new cluster %s", cluster.Name)

	kubernetesEndpoint, err := url.Parse(cluster.Spec.Connection.KubernetesAPIEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse kubernetes apiserver endpoint %s failed: %v", cluster.Spec.Connection.KubernetesAPIEndpoint, err)
	}
	kubesphereEndpoint, err := url.Parse(cluster.Spec.Connection.KubeSphereAPIEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse kubesphere apiserver endpoint %s failed: %v", cluster.Spec.Connection.KubeSphereAPIEndpoint, err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(cluster.Spec.Connection.KubeConfig)
	if err != nil {
		return nil, err
	}
	kubernetesClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	serverVersion, err := kubernetesClient.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}

	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, err
	}
	mapper, err := apiutil.NewDynamicRESTMapper(restConfig, httpClient)
	if err != nil {
		return nil, err
	}
	cacheClient, err := runtimecache.New(restConfig, runtimecache.Options{
		HTTPClient: httpClient,
		Scheme:     scheme.Scheme,
		Mapper:     mapper,
	})
	if err != nil {
		return nil, err
	}
	go cacheClient.Start(context.Background()) // nolint
	if !cacheClient.WaitForCacheSync(context.Background()) {
		return nil, errors.New("WaitForCacheSync failed")
	}

	client, err := runtimeclient.New(restConfig, runtimeclient.Options{
		HTTPClient: httpClient,
		Scheme:     scheme.Scheme,
		Mapper:     mapper,
		Cache: &runtimeclient.CacheOptions{
			Reader:       cacheClient,
			Unstructured: true,
		},
	})
	if err != nil {
		return nil, err
	}

	clusterClient := &ClusterClient{
		KubernetesURL:     kubernetesEndpoint,
		KubeSphereURL:     kubesphereEndpoint,
		KubernetesVersion: serverVersion.GitVersion,
		RestConfig:        restConfig,
		Transport:         httpClient.Transport,
		Client:            client,
		KubernetesClient:  kubernetesClient,
	}
	c.Lock()
	c.clients[cluster.Name] = clusterClient
	c.Unlock()
	return clusterClient, nil
}

func (c *clusterClients) removeCluster(obj interface{}) {
	cluster := obj.(*clusterv1alpha1.Cluster)
	klog.V(4).Infof("remove cluster %s", cluster.Name)
	c.Lock()
	delete(c.clients, cluster.Name)
	c.Unlock()
}

func (c *clusterClients) Get(clusterName string) (*clusterv1alpha1.Cluster, error) {
	cluster := &clusterv1alpha1.Cluster{}
	err := c.cache.Get(context.Background(), types.NamespacedName{Name: clusterName}, cluster)
	return cluster, err
}

func (c *clusterClients) ListClusters(ctx context.Context) ([]clusterv1alpha1.Cluster, error) {
	clusterList := &clusterv1alpha1.ClusterList{}
	if err := c.cache.List(ctx, clusterList); err != nil {
		return nil, err
	}
	return clusterList.Items, nil
}

func (c *clusterClients) GetClusterClient(name string) (*ClusterClient, error) {
	c.RLock()
	defer c.RUnlock()
	if client, ok := c.clients[name]; ok {
		return client, nil
	}

	// double check if the cluster exists but is not cached
	cluster, err := c.Get(name)
	if err != nil {
		return nil, err
	}
	return c.addCluster(cluster)
}

func (c *clusterClients) GetRuntimeClient(name string) (runtimeclient.Client, error) {
	clusterClient, err := c.GetClusterClient(name)
	if err != nil {
		return nil, err
	}
	return clusterClient.Client, nil
}
