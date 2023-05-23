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

	"k8s.io/apimachinery/pkg/types"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/models"
)

type Interface interface {
	GetKubectlPod(username string) (models.PodInfo, error)
}

type operator struct {
	client       runtimeclient.Client
	kubectlImage string
}

func NewOperator(cacheClient runtimeclient.Client, kubectlImage string) Interface {
	return &operator{
		client:       cacheClient,
		kubectlImage: kubectlImage,
	}
}

func (o *operator) GetKubectlPod(username string) (models.PodInfo, error) {
	if err := o.createKubectlPod(username); err != nil {
		return models.PodInfo{}, err
	}

	// wait for the pod to be ready
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	podName := fmt.Sprintf("%s-%s", constants.KubectlPodNamePrefix, username)
	if err := wait.PollImmediateUntil(2*time.Second, func() (bool, error) {
		pod := &corev1.Pod{}
		err := o.client.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: podName}, pod)
		if err != nil || !isPodReady(pod) {
			return false, err
		}
		return true, nil
	}, ctx.Done()); err != nil {
		return models.PodInfo{}, err
	}
	return models.PodInfo{Namespace: constants.KubeSphereNamespace, Pod: podName, Container: "kubectl"}, nil
}

func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (o *operator) createKubectlPod(username string) error {
	if err := o.client.Get(context.Background(), types.NamespacedName{Name: username}, &iamv1beta1.User{}); err != nil {
		// ignore if user not exist
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: constants.KubeSphereNamespace,
			Name:      fmt.Sprintf("%s-%s", constants.KubectlPodNamePrefix, username),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "kubectl",
					Image:           o.kubectlImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host-time",
							MountPath: "/etc/localtime",
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
			},
		},
	}

	if err := o.client.Create(context.Background(), pod); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}
