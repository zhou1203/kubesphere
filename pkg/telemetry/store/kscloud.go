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

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/models/marketplace"
)

const (
	// defaultTelemetryEndpoint for telemetry endpoint
	defaultTelemetryEndpoint = "/apis/telemetry/v1/clusterinfos?cluster_id=${cluster_id}"
)

// ksCloudStore send collector data to http endpoint
type ksCloudStore struct {
	// url for ksCloud
	serverURL string

	// client to get http config
	client runtimeclient.Client

	// telemetry endpoint
	telemetryEndpoint string
}

// KSCloudOptions for NewKSCloudStore
type KSCloudOptions struct {
	// ServerURL for ksCloud
	ServerURL *string

	// client to get kscloud config
	Client runtimeclient.Client

	// the end point to send telemetry data
	TelemetryEndpoint string
}

func NewKSCloudStore(opt KSCloudOptions) Store {
	if opt.TelemetryEndpoint == "" {
		opt.TelemetryEndpoint = defaultTelemetryEndpoint
	}

	return &ksCloudStore{
		serverURL:         *opt.ServerURL,
		client:            opt.Client,
		telemetryEndpoint: opt.TelemetryEndpoint,
	}
}

// Save store in cloud server
func (h ksCloudStore) Save(ctx context.Context, data map[string]interface{}) error {
	// get clusterId
	clusterId, err := h.GetHostClusterID(ctx, data)
	if err != nil {
		return err
	}

	// get cloudID
	if options, err := marketplace.LoadOptions(ctx, h.client); err != nil {
		klog.Warningf("connot get cloudID %s", err)
		data[cloudID] = ""
	} else {
		data[cloudID] = options.Account.UserID
	}

	// convert req data
	reqData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	telemtryReq := fmt.Sprintf(`{ "customer_id":"", "data": %s }`, string(reqData))
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s%s", h.serverURL, strings.ReplaceAll(h.telemetryEndpoint, "${cluster_id}", clusterId)), bytes.NewBufferString(telemtryReq))
	if err != nil {
		return err
	}
	// add token in header
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("resp code expect %v, but get code %v ", http.StatusOK, resp.StatusCode)
	}
	return nil
}

// GetHostClusterID form data. if it not exists, Get it from client
func (h ksCloudStore) GetHostClusterID(ctx context.Context, data map[string]interface{}) (string, error) {
	// get from data
	if clustersData, ok := data["clusters"]; ok {
		if clusters, ok := clustersData.([]interface{}); ok {
			for _, cluster := range clusters {
				if clmap, ok := cluster.(map[string]interface{}); ok {
					if clmap["role"] == "host" {
						return clmap["nid"].(string), nil
					}
				}
			}
		}
	}

	// get from client
	var clusterList = &clusterv1alpha1.ClusterList{}
	if err := h.client.List(ctx, clusterList); err != nil {
		return "", err
	}
	// statistics cluster Data
	for _, cluster := range clusterList.Items {
		if _, ok := cluster.Labels[clusterv1alpha1.HostCluster]; ok {
			return string(cluster.Status.UID), nil
		}
	}
	return "", fmt.Errorf("cannot get cluster_id")
}
