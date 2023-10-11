/*
Copyright 2019 The KubeSphere Authors.

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

package kubectl

import (
	"context"
	"fmt"
	"time"

	"kubesphere.io/kubesphere/pkg/models/terminal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models"
)

type Interface interface {
	GetKubectlPod(username string) (models.PodInfo, error)
}

type operator struct {
	client  runtimeclient.Client
	options *terminal.Options
}

func NewOperator(cacheClient runtimeclient.Client, options *terminal.Options) Interface {
	return &operator{
		client:  cacheClient,
		options: options,
	}
}

func (o *operator) GetKubectlPod(username string) (models.PodInfo, error) {
	pod := &corev1.Pod{}
	// wait for the pod to be ready
	if err := wait.PollUntilContextTimeout(context.Background(), time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := o.client.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: fmt.Sprintf("%s%s", constants.KubectlPodNamePrefix, username)}, pod); err != nil {
			if errors.IsNotFound(err) {
				if pod, err = o.createKubectlPod(ctx, username); err != nil {
					return false, err
				}
			}
			return false, err
		}
		if !pod.DeletionTimestamp.IsZero() {
			return false, nil
		}
		if !isPodReady(pod) {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return models.PodInfo{}, err
	}
	if !isPodReady(pod) {
		return models.PodInfo{}, fmt.Errorf("pod not ready")
	}
	return models.PodInfo{Namespace: pod.Namespace, Pod: pod.Name, Container: "kubectl"}, nil
}

func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (o *operator) createKubectlPod(ctx context.Context, username string) (*corev1.Pod, error) {
	if err := o.client.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("kubeconfig-%s", username), Namespace: constants.KubeSphereNamespace}, &corev1.Secret{}); err != nil {
		return nil, err
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: constants.KubeSphereNamespace,
			Name:      fmt.Sprintf("%s%s", constants.KubectlPodNamePrefix, username),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "kubectl",
					Image:           o.options.KubectlOptions.Image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host-time",
							MountPath: "/etc/localtime",
						},
						{
							Name:      "kubeconfig",
							MountPath: "/root/.kube/",
						},
					},
				},
			},
			ServiceAccountName: "kubesphere",
			Volumes: []corev1.Volume{
				{
					Name: "host-time",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/etc/localtime",
						},
					},
				},
				{
					Name: "kubeconfig",
					VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
						SecretName: fmt.Sprintf("kubeconfig-%s", username),
					}},
				},
			},
		},
	}
	if err := o.client.Create(ctx, pod); err != nil {
		return nil, err
	}
	return pod, nil
}
