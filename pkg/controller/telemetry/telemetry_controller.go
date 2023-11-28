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

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	telemetryv1alpha1 "kubesphere.io/api/telemetry/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/telemetry"
)

const (
	// defaultTelemetryEndpoint for telemetry endpoint
	defaultTelemetryEndpoint = "/apis/telemetry/v1/clusterinfos?cluster_id=${cluster_id}"
)

type Reconciler struct {
	*telemetry.Options
	runtimeclient.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	clusterInfo := &telemetryv1alpha1.ClusterInfo{}
	if err := r.Client.Get(ctx, request.NamespacedName, clusterInfo); err != nil {
		return reconcile.Result{}, err
	}
	// ignore delete resource
	if clusterInfo.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	// Clean up expired data.
	if clusterInfo.CreationTimestamp.Add(*r.Options.ClusterInfoLiveTime).Before(time.Now()) {
		if err := r.Client.Delete(ctx, clusterInfo); err != nil {
			klog.Errorf("delete expired clusterInfo %s error %s", request.Name, err)
			return reconcile.Result{}, nil
		}
	}

	// sync data to ksCloud.
	if clusterInfo.Status != nil && clusterInfo.Status.SyncTime == nil {
		if err := r.syncToKSCloud(ctx, clusterInfo); err != nil {
			klog.Errorf("sync clusterInfo %s to ksCloud error %s", request.Name, err)
			return reconcile.Result{}, nil
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) syncToKSCloud(ctx context.Context, clusterInfo *telemetryv1alpha1.ClusterInfo) error {
	data := clusterInfo.Status.DeepCopy()

	// get clusterId from data
	clusterId := ""
	for _, cluster := range data.Clusters {
		if cluster.Role == "host" {
			clusterId = cluster.Nid
		}
	}
	if clusterId == "" { // When the data has not been collected yet
		klog.Infof("clusterInfo %s clusterId is empty. skip sync", clusterInfo.Name)
		return nil
	}

	// convert req data
	reqData, err := json.Marshal(data)
	if err != nil {
		klog.Errorf("convert clusterInfo %s status to json error %v", clusterInfo.Name, err)
		return err
	}
	telemetryReq := fmt.Sprintf(`{ "user_id": "%s","data": %s }`, data.CloudId, string(reqData))
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s%s", *r.Options.KSCloudURL, strings.ReplaceAll(defaultTelemetryEndpoint, "${cluster_id}", clusterId)), bytes.NewBufferString(telemetryReq))
	if err != nil {
		klog.Errorf("new request for clusterInfo %s error %v", clusterInfo.Name, err)
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		klog.Errorf("do request for clusterInfo %s error %v", clusterInfo.Name, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("resp code expect %v, but get code %v ", http.StatusOK, resp.StatusCode)
	}

	// add annotation to clusterInfo
	newClusterInfo := clusterInfo.DeepCopy()
	// sync to ksCloud
	now := metav1.Now()
	newClusterInfo.Status.SyncTime = &now
	if err := r.Client.Status().Patch(ctx, newClusterInfo, runtimeclient.MergeFrom(clusterInfo.DeepCopy())); err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// init Reconciler
	r.Client = mgr.GetClient()

	// start telemetry
	if err := mgr.Add(telemetry.NewTelemetry(
		telemetry.WithClient(mgr.GetClient()),
		telemetry.WithOptions(r.Options)),
	); err != nil {
		return err
	}

	// start watch ClusterInfo
	return builder.
		ControllerManagedBy(mgr).
		For(&telemetryv1alpha1.ClusterInfo{}).
		Complete(r)
}
