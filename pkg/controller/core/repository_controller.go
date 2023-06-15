/*
Copyright 2022 KubeSphere Authors

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

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/repo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	"kubesphere.io/utils/helm"

	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	RepositoryFinalizer         = "repositories.kubesphere.io"
	minimumRegistryPollInterval = 15 * time.Minute
	defaultRequeueInterval      = 15 * time.Second
	repositoryController        = "repository-controller"
	generateNameFormat          = "repository-%s"
)

var _ reconcile.Reconciler = &RepositoryReconciler{}

type RepositoryReconciler struct {
	client.Client
	recorder record.EventRecorder
	logger   logr.Logger
}

// reconcileDelete delete the repository and pod.
func (r *RepositoryReconciler) reconcileDelete(ctx context.Context, repo *corev1alpha1.Repository) (ctrl.Result, error) {
	// Remove the finalizer from the subscription and update it.
	controllerutil.RemoveFinalizer(repo, RepositoryFinalizer)
	if err := r.Update(ctx, repo); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// createOrUpdateExtension create a new extension if the extension does not exist.
// Or it will update info of the extension.
func (r *RepositoryReconciler) createOrUpdateExtension(ctx context.Context, repo *corev1alpha1.Repository, extensionName string, latestExtensionVersion *corev1alpha1.ExtensionVersion) (*corev1alpha1.Extension, error) {
	logger := klog.FromContext(ctx)
	extension := &corev1alpha1.Extension{}
	if err := r.Get(ctx, types.NamespacedName{Name: extensionName}, extension); err != nil {
		if apierrors.IsNotFound(err) {
			extension = &corev1alpha1.Extension{
				ObjectMeta: metav1.ObjectMeta{
					Name: extensionName,
					Labels: map[string]string{
						corev1alpha1.RepositoryReferenceLabel: repo.Name,
					},
				},
			}
			extension.Spec = corev1alpha1.ExtensionSpec{
				ExtensionInfo: &corev1alpha1.ExtensionInfo{
					DisplayName: latestExtensionVersion.Spec.DisplayName,
					Description: latestExtensionVersion.Spec.Description,
					Icon:        latestExtensionVersion.Spec.Icon,
					Provider:    latestExtensionVersion.Spec.Provider,
				},
			}
			if err := controllerutil.SetOwnerReference(repo, extension, r.Scheme()); err != nil {
				return nil, err
			}
			logger.V(4).Info("create extension", "repo", repo.Name, "extension", extensionName)
			if err := r.Create(ctx, extension, &client.CreateOptions{}); err != nil {
				logger.Error(err, "failed to create extension", "repo", repo.Name, "extension", extensionName)
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// conflict extension
	if extension.ObjectMeta.Labels[corev1alpha1.RepositoryReferenceLabel] != repo.Name {
		logger.Info("conflict extension found", "repo", extension.ObjectMeta.Labels[corev1alpha1.RepositoryReferenceLabel], "extension", extensionName)
		return nil, nil
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extensionName}, extension); err != nil {
			return err
		}
		extension = extension.DeepCopy()
		extension.Spec = corev1alpha1.ExtensionSpec{
			ExtensionInfo: &corev1alpha1.ExtensionInfo{
				DisplayName: latestExtensionVersion.Spec.DisplayName,
				Description: latestExtensionVersion.Spec.Description,
				Icon:        latestExtensionVersion.Spec.Icon,
				Provider:    latestExtensionVersion.Spec.Provider,
			},
		}
		return r.Update(ctx, extension)
	})

	if err != nil {
		return nil, err
	}

	return extension, nil
}

func (r *RepositoryReconciler) createOrUpdateExtensionVersion(ctx context.Context, repo *corev1alpha1.Repository, extension *corev1alpha1.Extension, extensionVersion *corev1alpha1.ExtensionVersion) error {
	logger := klog.FromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		version := &corev1alpha1.ExtensionVersion{}
		if err := r.Get(ctx, types.NamespacedName{Name: extensionVersion.Name}, version); err != nil {
			if apierrors.IsNotFound(err) {
				if err := controllerutil.SetOwnerReference(extension, extensionVersion, r.Scheme()); err != nil {
					return err
				}
				logger.V(4).Info("create extension version", "repo", repo.Name, "version", extensionVersion.Name)
				if err := r.Create(ctx, extensionVersion, &client.CreateOptions{}); err != nil {
					logger.Error(err, "failed to create extension version", "repo", repo.Name, "version", extensionVersion.Name)
					return err
				}
				return nil
			} else {
				return err
			}
		}
		if !reflect.DeepEqual(version.Spec, extensionVersion.Spec) {
			version.Spec = extensionVersion.Spec
			logger.V(4).Info("update extension version", "repo", repo.Name, "version", extensionVersion.Name)
			if err := r.Update(ctx, version); err != nil {
				logger.Error(err, "failed to update extension version", "repo", repo.Name, "version", extensionVersion.Name)
				return err
			}
		}
		return nil
	})
}

func (r *RepositoryReconciler) syncExtensions(ctx context.Context, index *repo.IndexFile, repo *corev1alpha1.Repository) error {
	logger := klog.FromContext(ctx)
	for extensionName, versions := range index.Entries {
		extensionVersions := make([]corev1alpha1.ExtensionVersion, 0, len(versions))
		for _, version := range versions {
			if version.Metadata == nil {
				logger.Info("version metadata is empty", "repo", repo.Name)
				continue
			}

			var provider map[corev1alpha1.LanguageCode]*corev1alpha1.Provider
			if len(version.Maintainers) > 0 {
				maintainer := version.Maintainers[0]
				provider = map[corev1alpha1.LanguageCode]*corev1alpha1.Provider{
					corev1alpha1.DefaultLanguageCode: {
						Name:  maintainer.Name,
						URL:   maintainer.URL,
						Email: maintainer.Email,
					},
				}
			}
			var chartURL string
			if len(version.URLs) > 0 {
				chartURL = version.URLs[0]
			}

			var description corev1alpha1.Locales
			if version.Description != "" {
				if err := json.Unmarshal([]byte(version.Description), &description); err != nil {
					description = corev1alpha1.Locales{
						corev1alpha1.DefaultLanguageCode: corev1alpha1.LocaleString(version.Description),
					}
				}
			}

			var displayName corev1alpha1.Locales
			if displayNameAnnotation := version.Annotations[corev1alpha1.DisplayNameAnnotation]; displayNameAnnotation != "" {
				if err := json.Unmarshal([]byte(displayNameAnnotation), &displayName); err != nil {
					displayName = corev1alpha1.Locales{
						corev1alpha1.DefaultLanguageCode: corev1alpha1.LocaleString(displayNameAnnotation),
					}
				}
			}

			var installationMode corev1alpha1.InstallationMode
			if version.Annotations[corev1alpha1.InstallationModeAnnotation] == corev1alpha1.InstallationMulticluster {
				installationMode = corev1alpha1.InstallationMulticluster
			} else {
				installationMode = corev1alpha1.InstallationModeHostOnly
			}

			var externalDependencies []corev1alpha1.ExternalDependency
			if externalDependenciesAnnotation := version.Annotations[corev1alpha1.ExternalDependenciesAnnotation]; externalDependenciesAnnotation != "" {
				if err := json.Unmarshal([]byte(externalDependenciesAnnotation), &externalDependencies); err != nil {
					logger.V(4).Info("invalid external dependencies annotation", "annotation", externalDependenciesAnnotation)
				}
			}

			ksVersion := version.Annotations[corev1alpha1.KSVersionAnnotation]
			extensionVersions = append(extensionVersions, corev1alpha1.ExtensionVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s-%s", version.Name, version.Version),
					Labels: map[string]string{
						corev1alpha1.RepositoryReferenceLabel: repo.Name,
						corev1alpha1.ExtensionReferenceLabel:  extensionName,
					},
				},
				Spec: corev1alpha1.ExtensionVersionSpec{
					Keywords:         version.Keywords,
					Repository:       repo.Name,
					Version:          version.Version,
					KubeVersion:      version.KubeVersion,
					KSVersion:        ksVersion,
					Home:             version.Home,
					Sources:          version.Sources,
					InstallationMode: installationMode,
					ExtensionInfo: &corev1alpha1.ExtensionInfo{
						DisplayName: displayName,
						Description: description,
						Icon:        version.Icon,
						Provider:    provider,
					},
					ChartURL:             chartURL,
					ExternalDependencies: externalDependencies,
				},
			})
		}

		latestExtensionVersion := getLatestExtensionVersion(extensionVersions)
		if latestExtensionVersion == nil {
			continue
		}
		extension, err := r.createOrUpdateExtension(ctx, repo, extensionName, latestExtensionVersion)
		if err != nil {
			return err
		}
		// ignore conflict
		if extension == nil {
			continue
		}
		for _, extensionVersion := range extensionVersions {
			if err := r.createOrUpdateExtensionVersion(ctx, repo, extension, &extensionVersion); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *RepositoryReconciler) syncExtensionsFromURL(ctx context.Context, repo *corev1alpha1.Repository, url string) error {
	newCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// TODO support TLS and auth
	index, err := helm.LoadRepoIndex(newCtx, url, helm.RepoCredential{})
	if err != nil {
		return err
	}
	return r.syncExtensions(ctx, index, repo)
}

func (r *RepositoryReconciler) reconcileRepository(ctx context.Context, repo *corev1alpha1.Repository) (ctrl.Result, error) {
	registryPollInterval := minimumRegistryPollInterval
	if repo.Spec.UpdateStrategy != nil && repo.Spec.UpdateStrategy.Interval.Duration > minimumRegistryPollInterval {
		registryPollInterval = repo.Spec.UpdateStrategy.Interval.Duration
	}

	var repoURL string
	// URL and Image are immutable after creation
	if repo.Spec.URL != "" {
		repoURL = repo.Spec.URL
	} else if repo.Spec.Image != "" {
		var deployment appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: fmt.Sprintf(generateNameFormat, repo.Name)}, &deployment); err != nil {
			if apierrors.IsNotFound(err) {
				if err := r.deployRepository(ctx, repo); err != nil {
					r.recorder.Eventf(repo, corev1.EventTypeWarning, "RepositoryDeployFailed", err.Error())
					return ctrl.Result{}, fmt.Errorf("failed to deploy repository: %s", err)
				}
				r.recorder.Eventf(repo, corev1.EventTypeNormal, "RepositoryDeployed", "")
				return ctrl.Result{Requeue: true, RequeueAfter: defaultRequeueInterval}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to fetch deployment: %s", err)
		}

		restartAt, _ := time.Parse(time.RFC3339, deployment.Spec.Template.Annotations["kubesphere.io/restartedAt"])
		if restartAt.IsZero() {
			restartAt = deployment.ObjectMeta.CreationTimestamp.Time
		}
		// restart and pull the latest docker image
		if time.Now().After(repo.Status.LastSyncTime.Add(registryPollInterval)) && time.Now().After(restartAt.Add(registryPollInterval)) {
			rawData := []byte(fmt.Sprintf("{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"kubesphere.io/restartedAt\":\"%s\"}}}}}", time.Now().Format(time.RFC3339)))
			if err := r.Patch(ctx, &deployment, client.RawPatch(types.StrategicMergePatchType, rawData)); err != nil {
				return ctrl.Result{}, err
			}
			r.recorder.Eventf(repo, corev1.EventTypeNormal, "RepositoryRestarted", "")
			return ctrl.Result{Requeue: true, RequeueAfter: defaultRequeueInterval}, nil
		}

		if deployment.Status.AvailableReplicas != deployment.Status.Replicas {
			return ctrl.Result{Requeue: true, RequeueAfter: defaultRequeueInterval}, nil
		}

		// TODO support custom port
		// ready to sync
		repoURL = fmt.Sprintf("http://%s.%s.svc", deployment.Name, constants.KubeSphereNamespace)
	}

	if repoURL != "" && time.Now().After(repo.Status.LastSyncTime.Add(registryPollInterval)) {
		if err := r.syncExtensionsFromURL(ctx, repo, repoURL); err != nil {
			r.recorder.Eventf(repo, corev1.EventTypeWarning, "ExtensionsSyncFailed", err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to sync extensions: %s", err)
		}
		r.recorder.Eventf(repo, corev1.EventTypeNormal, "RepositorySynced", "")
		repo = repo.DeepCopy()
		repo.Status.LastSyncTime = metav1.Now()
		if err := r.Update(ctx, repo); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update repository: %s", err)
		}
	}

	return ctrl.Result{Requeue: true, RequeueAfter: registryPollInterval}, nil
}

func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("repository", req.String())
	logger.V(4).Info("sync repository")
	ctx = klog.NewContext(ctx, logger)

	repo := &corev1alpha1.Repository{}
	if err := r.Client.Get(ctx, req.NamespacedName, repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(repo, RepositoryFinalizer) {
		expected := repo.DeepCopy()
		controllerutil.AddFinalizer(expected, RepositoryFinalizer)
		return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(repo))
	}

	if !repo.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, repo)
	} else {
		return r.reconcileRepository(ctx, repo)
	}
}

func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(subscriptionController)
	r.recorder = mgr.GetEventRecorderFor(repositoryController)
	return ctrl.NewControllerManagedBy(mgr).
		Named(repositoryController).
		For(&corev1alpha1.Repository{}).Complete(r)
}

func (r *RepositoryReconciler) deployRepository(ctx context.Context, repo *corev1alpha1.Repository) error {
	generateName := fmt.Sprintf(generateNameFormat, repo.Name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName,
			Namespace: constants.KubeSphereNamespace,
			Labels:    map[string]string{corev1alpha1.RepositoryReferenceLabel: repo.Name},
		},

		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{corev1alpha1.RepositoryReferenceLabel: repo.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{corev1alpha1.RepositoryReferenceLabel: repo.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "repository",
							Image:           repo.Spec.Image,
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{
									Name:  "CHART_URL",
									Value: fmt.Sprintf("http://%s.%s.svc", generateName, constants.KubeSphereNamespace),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt(8080),
									},
								},
								PeriodSeconds:       10,
								InitialDelaySeconds: 5,
							},
						},
					},
				},
			},
		},
	}

	if err := controllerutil.SetOwnerReference(repo, deployment, r.Scheme()); err != nil {
		return err
	}
	if err := r.Create(ctx, deployment); err != nil {
		return err
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName,
			Namespace: constants.KubeSphereNamespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt(8080),
				},
			},
			Selector: map[string]string{
				corev1alpha1.RepositoryReferenceLabel: repo.Name,
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if err := controllerutil.SetOwnerReference(repo, service, r.Scheme()); err != nil {
		return err
	}

	if err := r.Create(ctx, service); err != nil {
		return err
	}

	return nil
}
