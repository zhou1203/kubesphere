/*
Copyright 2020 The KubeSphere Authors.

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
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
)

func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return builder.WebhookManagedBy(mgr).
		For(&clusterv1alpha1.Cluster{}).
		WithValidator(&clusterValidator{}).
		Complete()
}

type clusterValidator struct{}

func (v *clusterValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *clusterValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldCluster, ok := oldObj.(*clusterv1alpha1.Cluster)
	if !ok {
		return nil, fmt.Errorf("expected a Cluster but got a %T", oldObj)
	}
	newCluster, ok := newObj.(*clusterv1alpha1.Cluster)
	if !ok {
		return nil, fmt.Errorf("expected a Cluster but got a %T", newObj)
	}

	// The cluster created for the first time has no status information
	if oldCluster.Status.UID == "" {
		return nil, nil
	}

	clusterConfig, err := clientcmd.RESTConfigFromKubeConfig(newCluster.Spec.Connection.KubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster config for %s: %s", newCluster.Name, err)
	}
	clusterClient, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}
	kubeSystem, err := clusterClient.CoreV1().Namespaces().Get(ctx, metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if oldCluster.Status.UID != kubeSystem.UID {
		return nil, errors.New("this kubeconfig corresponds to a different cluster than the previous one, you need to make sure that kubeconfig is not from another cluster")
	}
	return nil, nil
}

func (v *clusterValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
