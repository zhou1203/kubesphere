/*
Copyright 2022 The KubeSphere Authors.

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

package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	helmrelease "helm.sh/helm/v3/pkg/release"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/kustomize/api/types"
)

// Executor is used to manage a helm release, you can install/uninstall and upgrade a chart
// or get the status and manifest data of the release, etc.
type Executor interface {
	// Install installs the specified chart and returns the name of the Job that executed the task.
	Install(ctx context.Context, chartName string, chartData, values []byte, options ...HelmOption) (string, error)
	// Upgrade upgrades the specified chart and returns the name of the Job that executed the task.
	Upgrade(ctx context.Context, chartName string, chartData, values []byte, options ...HelmOption) (string, error)
	// Uninstall is used to uninstall the specified chart and returns the name of the Job that executed the task.
	Uninstall(ctx context.Context, options ...HelmOption) (string, error)
	// ForceDelete forcibly deletes all resources of the chart.
	ForceDelete(ctx context.Context, options ...HelmOption) error
	// Manifest returns the manifest data for this release.
	Manifest(options ...HelmOption) (string, error)
	// IsReleaseReady checks if the helm release is ready.
	IsReleaseReady(timeout time.Duration, options ...HelmOption) (bool, error)
}

const (
	workspaceBaseSource = "/tmp/helm-executor-source"
	workspaceBase       = "/tmp/helm-executor"

	statusNotFoundFormat = "release: not found"
	releaseExists        = "release exists"

	kustomizationFile  = "kustomization.yaml"
	postRenderExecFile = "helm-post-render.sh"
	// kustomize cannot read stdio now, so we save helm stdout to file, then kustomize reads that file and build the resources
	kustomizeBuild = `#!/bin/sh
# save helm stdout to file, then kustomize read this file
cat > ./.local-helm-output.yaml
kustomize build
`
	kubeConfigPath = "kube.config"
)

var (
	errorTimedOutToWaitResource = errors.New("timed out waiting for resources to be ready")
)

type executor struct {
	// target cluster client
	client    kubernetes.Interface
	namespace string
	// helm release name
	releaseName string

	helmImage string
	// add labels to helm chart
	labels map[string]string
	// add annotations to helm chart
	annotations     map[string]string
	createNamespace bool
	dryRun          bool
}

type Option func(*executor)

// SetDryRun sets the dryRun option.
func SetDryRun(dryRun bool) Option {
	return func(e *executor) {
		e.dryRun = dryRun
	}
}

// SetAnnotations sets extra annotations added to all resources in chart.
func SetAnnotations(annotations map[string]string) Option {
	return func(e *executor) {
		e.annotations = annotations
	}
}

// SetLabels sets extra labels added to all resources in chart.
func SetLabels(labels map[string]string) Option {
	return func(e *executor) {
		e.labels = labels
	}
}

// SetHelmImage sets the helmImage option.
func SetHelmImage(helmImage string) Option {
	return func(e *executor) {
		e.helmImage = helmImage
	}
}

// SetCreateNamespace sets the createNamespace option.
func SetCreateNamespace(createNamespace bool) Option {
	return func(e *executor) {
		e.createNamespace = createNamespace
	}
}

// NewExecutor generates a new Executor instance with the following parameters:
//   - kubeConfig: this kube config is used to create the necessary Namespace, ConfigMap and Job to perform
//     the installation tasks during the installation. You only need to give the kube config the necessary permissions,
//     if the kube config is not set, we will use the in-cluster config, this may not work.
//   - namespace: the namespace of the helm release
//   - releaseName: the helm release name
//   - options: functions to set optional parameters
func NewExecutor(kubeConfig, namespace, releaseName string, options ...Option) (Executor, error) {
	e := &executor{
		namespace:   namespace,
		releaseName: releaseName,
		helmImage:   "kubesphere/helm:latest",
	}
	for _, option := range options {
		option(e)
	}

	var (
		restConfig *rest.Config
		err        error
	)
	if kubeConfig == "" {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	} else {
		restConfig, err = clientcmd.RESTConfigFromKubeConfig([]byte(kubeConfig))
		if err != nil {
			return nil, err
		}
	}
	clusterClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	e.client = clusterClient

	klog.V(8).Infof("namespace: %s, release name: %s, kube config:%s", e.namespace, e.releaseName, kubeConfig)

	if e.createNamespace {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: e.namespace,
			},
		}
		if _, err = e.client.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return e, err
		}
	}
	return e, nil
}

type helmOption struct {
	kubeConfig string
	debug      bool
}

type HelmOption func(*helmOption)

// SetHelmKubeConfig sets the kube config data of the target cluster used by helm installation.
// NOTE: this kube config is used by the helm command to create specific chart resources.
// You only need to give the kube config the permissions it needs in the target namespace,
// if the kube config is not set, we will use the in-cluster config, this may not work.
func SetHelmKubeConfig(kubeConfig string) HelmOption {
	return func(o *helmOption) {
		o.kubeConfig = kubeConfig
	}
}

// SetHelmDebug adds `--debug` argument to helm command.
func SetHelmDebug(debug bool) HelmOption {
	return func(o *helmOption) {
		o.debug = debug
	}
}

func initHelmConf(kubeConfig, namespace string) (*action.Configuration, error) {
	getter := NewClusterRESTClientGetter(kubeConfig, namespace)
	helmConf := new(action.Configuration)
	if err := helmConf.Init(getter, namespace, "", klog.Infof); err != nil {
		return nil, err
	}
	return helmConf, nil
}

// Install installs the specified chart, returns the name of the Job that executed the task.
func (e *executor) Install(ctx context.Context, chartName string, chartData, values []byte, options ...HelmOption) (string, error) {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return "", err
	}

	sts, err := e.status(helmConf)
	if err == nil {
		// helm release has been installed
		if sts.Info != nil && sts.Info.Status == "deployed" {
			return "", nil
		}
		return "", errors.New(releaseExists)
	} else {
		if err.Error() == statusNotFoundFormat {
			// continue to install
			return e.createInstallJob(ctx, helmOptions.kubeConfig, chartName, chartData, values, false, helmOptions.debug)
		}
		return "", err
	}
}

// Upgrade upgrades the specified chart, returns the name of the Job that executed the task.
func (e *executor) Upgrade(ctx context.Context, chartName string, chartData, values []byte, options ...HelmOption) (string, error) {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return "", err
	}

	sts, err := e.status(helmConf)
	if err != nil {
		return "", err
	}

	if sts.Info.Status == "deployed" {
		return e.createInstallJob(ctx, helmOptions.kubeConfig, chartName, chartData, values, true, helmOptions.debug)
	}
	return "", fmt.Errorf("cannot upgrade release %s/%s, current state is %s", e.namespace, e.releaseName, sts.Info.Status)
}

func (e *executor) chartPath(chartName string) string {
	return fmt.Sprintf("%s.tgz", chartName)
}

func (e *executor) setupChartData(kubeConfig, chartName string, chartData, values []byte) (map[string][]byte, error) {
	kustomizationConfig := types.Kustomization{
		Resources:         []string{"./.local-helm-output.yaml"},
		CommonAnnotations: e.annotations,                    // add extra annotations to output
		Labels:            []types.Label{{Pairs: e.labels}}, // Labels to add to all objects but not selectors.
	}
	kustomizationData, err := yaml.Marshal(kustomizationConfig)
	if err != nil {
		return nil, err
	}

	data := map[string][]byte{
		postRenderExecFile:     []byte(kustomizeBuild),
		kustomizationFile:      kustomizationData,
		e.chartPath(chartName): chartData,
		"values.yaml":          values,
	}
	if kubeConfig != "" {
		data[kubeConfigPath] = []byte(kubeConfig)
	}
	return data, nil
}

func generateName(name string) string {
	return fmt.Sprintf("helm-executor-%s-%s", name, rand.String(6))
}

func (e *executor) createConfigMap(ctx context.Context, kubeConfig, chartName string, chartData, values []byte) (string, error) {
	data, err := e.setupChartData(kubeConfig, chartName, chartData, values)
	if err != nil {
		return "", err
	}

	name := generateName(chartName)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: e.namespace,
		},
		// we can't use `Data` here because creating it with client-go will cause our compressed file to be in the
		// wrong format (application/octet-stream)
		BinaryData: data,
	}
	if _, err = e.client.CoreV1().ConfigMaps(e.namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		return "", err
	}
	return name, nil
}

func (e *executor) createInstallJob(ctx context.Context, kubeConfig, chartName string, chartData, values []byte, upgrade, debug bool) (string, error) {
	args := make([]string, 0, 10)
	if upgrade {
		args = append(args, "upgrade")
	} else {
		args = append(args, "install")
	}

	args = append(args, "--wait", e.releaseName, e.chartPath(chartName), "--namespace", e.namespace)

	if len(values) > 0 {
		args = append(args, "--values", "values.yaml")
	}

	if e.dryRun {
		args = append(args, "--dry-run")
	}

	if kubeConfig != "" {
		args = append(args, "--kubeconfig", kubeConfigPath)
	}

	// Post render, add annotations or labels to resources
	if len(e.labels) > 0 || len(e.annotations) > 0 {
		args = append(args, "--post-renderer", filepath.Join(workspaceBase, postRenderExecFile))
	}

	if debug {
		// output debug info
		args = append(args, "--debug")
	}

	name, err := e.createConfigMap(ctx, kubeConfig, chartName, chartData, values)
	if err != nil {
		return "", err
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: e.namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: pointer.Int32Ptr(1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "helm",
							Image:           e.helmImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/bin/sh", "-c",
								fmt.Sprintf("cp -r %s/. %s && helm %s", workspaceBaseSource, workspaceBase, strings.Join(args, " ")),
							},
							WorkingDir: workspaceBase,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "source",
									MountPath: workspaceBaseSource,
								},
								{
									Name:      "data",
									MountPath: workspaceBase,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "source",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: name,
									},
									DefaultMode: pointer.Int32Ptr(0755),
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					RestartPolicy:                 corev1.RestartPolicyNever,
					TerminationGracePeriodSeconds: new(int64),
				},
			},
		},
	}

	if _, err = e.client.BatchV1().Jobs(e.namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return "", err
	}
	return name, nil
}

// Uninstall uninstalls the specified chart, returns the name of the Job that executed the task.
func (e *executor) Uninstall(ctx context.Context, options ...HelmOption) (string, error) {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return "", err
	}

	if _, err = e.status(helmConf); err != nil && err.Error() == statusNotFoundFormat {
		// already uninstalled
		return "", nil
	}

	args := []string{
		"uninstall",
		e.releaseName,
		"--namespace",
		e.namespace,
	}
	if e.dryRun {
		args = append(args, "--dry-run")
	}

	if helmOptions.debug {
		args = append(args, "--debug")
	}

	name := generateName(e.releaseName)
	if helmOptions.kubeConfig != "" {
		args = append(args, "--kubeconfig", kubeConfigPath)

		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: e.namespace,
			},
			Data: map[string]string{
				kubeConfigPath: helmOptions.kubeConfig,
			},
		}
		if _, err = e.client.CoreV1().ConfigMaps(e.namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
			return "", err
		}
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: e.namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: pointer.Int32Ptr(1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "helm",
							Image:           e.helmImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"helm"},
							Args:            args,
							WorkingDir:      workspaceBase,
						},
					},
					RestartPolicy:                 corev1.RestartPolicyNever,
					TerminationGracePeriodSeconds: new(int64),
				},
			},
		},
	}
	if helmOptions.kubeConfig != "" {
		job.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: name,
						},
						DefaultMode: pointer.Int32Ptr(0755),
					},
				},
			},
		}
		job.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: workspaceBase,
			},
		}
	}

	if _, err = e.client.BatchV1().Jobs(e.namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return "", err
	}
	return name, nil
}

// ForceDelete forcibly deletes all resources of the chart.
// The current implementation still uses the helm command to force deletion.
func (e *executor) ForceDelete(ctx context.Context, options ...HelmOption) error {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return err
	}

	if _, err = e.status(helmConf); err != nil && err.Error() == statusNotFoundFormat {
		// already uninstalled
		return nil
	}

	uninstall := action.NewUninstall(helmConf)
	uninstall.DisableHooks = true
	if _, err = uninstall.Run(e.releaseName); err != nil {
		return err
	}
	return nil
}

// Manifest returns the manifest data for this release.
func (e *executor) Manifest(options ...HelmOption) (string, error) {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return "", err
	}
	get := action.NewGet(helmConf)
	rel, err := get.Run(e.releaseName)
	if err != nil {
		klog.Errorf("namespace: %s, name: %s, run command failed, error: %v", e.namespace, e.releaseName, err)
		return "", err
	}
	klog.V(2).Infof("namespace: %s, name: %s, run command success", e.namespace, e.releaseName)
	klog.V(8).Infof("namespace: %s, name: %s, run command success, manifest: %s", e.namespace, e.releaseName, rel.Manifest)
	return rel.Manifest, nil
}

// IsReleaseReady checks if the helm release is ready.
func (e *executor) IsReleaseReady(timeout time.Duration, options ...HelmOption) (bool, error) {
	helmOptions := &helmOption{}
	for _, f := range options {
		f(helmOptions)
	}

	helmConf, err := initHelmConf(helmOptions.kubeConfig, e.namespace)
	if err != nil {
		return false, err
	}

	// Get the manifest to build resources
	get := action.NewGet(helmConf)
	rel, err := get.Run(e.releaseName)
	if err != nil {
		return false, err
	}

	kubeClient := helmConf.KubeClient
	resources, _ := kubeClient.Build(bytes.NewBufferString(rel.Manifest), true)

	err = kubeClient.Wait(resources, timeout)
	if err == nil {
		return true, nil
	}
	if err == wait.ErrWaitTimeout {
		return false, errorTimedOutToWaitResource
	}
	return false, err
}

func (e *executor) status(helmConf *action.Configuration) (*helmrelease.Release, error) {
	helmStatus := action.NewStatus(helmConf)
	rel, err := helmStatus.Run(e.releaseName)
	if err != nil {
		if err.Error() == statusNotFoundFormat {
			klog.V(2).Infof("namespace: %s, name: %s, run command failed, error: %v", e.namespace, e.releaseName, err)
			return nil, err
		}
		klog.Errorf("namespace: %s, name: %s, run command failed, error: %v", e.namespace, e.releaseName, err)
		return nil, err
	}

	klog.V(2).Infof("namespace: %s, name: %s, run command success", e.namespace, e.releaseName)
	klog.V(8).Infof("namespace: %s, name: %s, run command success, manifest: %s", e.namespace, e.releaseName, rel.Manifest)
	return rel, nil
}
