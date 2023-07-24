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
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	telemetryv1alpha1 "kubesphere.io/api/telemetry/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/models/marketplace"
)

const defaultNameFormat = "20060102150405"

// clusterInfoStore use clusterinfos.telemetry.kubesphere.io to store. one collect data use one cluster info resource
type clusterInfoStore struct {
	client   client.Client
	liveTime *time.Duration
}

func (c *clusterInfoStore) Save(ctx context.Context, data map[string]interface{}) error {
	// get cloudID
	if options, err := marketplace.LoadOptions(ctx, c.client); err != nil {
		klog.Warningf("connot get cloudID %s", err)
		data[cloudID] = ""
	} else {
		data[cloudID] = options.Account.UserID
	}
	status := &telemetryv1alpha1.ClusterInfoStatus{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(data, status); err != nil {
		return err
	}
	obj := &telemetryv1alpha1.ClusterInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: status.TotalTime.UTC().Format(defaultNameFormat),
		},
	}
	if err := c.create(ctx, obj); err != nil {
		return err
	}
	if err := c.updateStatus(ctx, obj, *status); err != nil {
		return err
	}
	return nil
}

func (c *clusterInfoStore) create(ctx context.Context, clusterInfo *telemetryv1alpha1.ClusterInfo) error {
	return c.client.Create(ctx, clusterInfo.DeepCopy())
}

func (c *clusterInfoStore) updateStatus(ctx context.Context, clusterInfo *telemetryv1alpha1.ClusterInfo, status telemetryv1alpha1.ClusterInfoStatus) error {
	newClusterInfo := clusterInfo.DeepCopy()
	newClusterInfo.Status = status
	return c.client.Status().Patch(ctx, newClusterInfo, client.MergeFrom(clusterInfo.DeepCopy()))
}

// ClusterInfoOptions for NewClusterInfoStore
type ClusterInfoOptions struct {
	LiveTime *time.Duration
	Client   client.Client
}

func NewClusterInfoStore(opt ClusterInfoOptions) Store {
	return &clusterInfoStore{
		liveTime: opt.LiveTime,
		client:   opt.Client,
	}
}
