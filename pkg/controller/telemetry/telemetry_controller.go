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
	"context"
	"time"

	telemetryv1alpha1 "kubesphere.io/api/telemetry/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/telemetry"
)

type Reconciler struct {
	*telemetry.Options
	runtimeClient.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	clusterInfo := &telemetryv1alpha1.ClusterInfo{}
	if err := r.Client.Get(ctx, request.NamespacedName, clusterInfo); err != nil {
		return reconcile.Result{}, err
	}
	// Clean up expired data.
	if clusterInfo.DeletionTimestamp == nil && clusterInfo.CreationTimestamp.Add(*r.Options.ClusterInfoLiveTime).Before(time.Now()) {
		if err := r.Client.Delete(ctx, clusterInfo); err != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, nil
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
