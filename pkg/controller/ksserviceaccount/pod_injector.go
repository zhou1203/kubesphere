package ksserviceaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
)

const (
	AnnotationServiceAccountName = "kubesphere.io/serviceaccount-name"
	ServiceAccountVolumeName     = "kubesphere-service-account"
	VolumeMountPath              = "/var/run/secrets/kubesphere.io/serviceaccount"
)

const webhookName = "service-account-injector"

func (w *Webhook) Name() string {
	return webhookName
}

var _ kscontroller.Controller = &Webhook{}

type Webhook struct {
}

func (w *Webhook) SetupWithManager(mgr *kscontroller.Manager) error {
	serviceAccountPodInjector := &PodInjector{
		Log:     mgr.GetLogger(),
		Decoder: admission.NewDecoder(mgr.GetScheme()),
		Client:  mgr.GetClient(),
	}
	mgr.GetWebhookServer().Register("/serviceaccount-pod-injector", &webhook.Admission{Handler: serviceAccountPodInjector})
	return nil
}

// PodInjector injects a token (issue by KubeSphere) by mounting a secret to the
// pod when the pod specifies a KubeSphere service account.
type PodInjector struct {
	Log     logr.Logger
	Decoder *admission.Decoder
	client.Client
}

func (i *PodInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := i.Log.WithValues("namespace", req.Namespace).WithValues("pod", req.Name)
	var saName string
	pod := &v1.Pod{}
	err := i.Decoder.Decode(req, pod)
	if err != nil {
		log.Error(err, "decode pod failed")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if pod.Annotations != nil {
		saName = pod.Annotations[AnnotationServiceAccountName]
	}

	if saName == "" {
		log.V(6).Info("pod does not specify kubesphere service account, skip it")
		return admission.Allowed("")
	}

	sa := &corev1alpha1.ServiceAccount{}
	err = i.Client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: saName}, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			return admission.Errored(http.StatusNotFound, err)
		}
		return admission.Errored(http.StatusInternalServerError, err)
	}

	err = i.injectServiceAccountToken(ctx, pod, sa)
	if err != nil {
		log.WithValues("serviceaccount", sa.Name).
			Error(err, "inject service account token to pod failed")
		if errors.IsNotFound(err) {
			return admission.Errored(http.StatusNotFound, err)
		}
		return admission.Errored(http.StatusInternalServerError, err)
	}
	log.V(6).WithValues("serviceaccount", sa.Name).
		Info("successfully injected service account token into the pod")

	marshal, err := json.Marshal(pod)
	if err != nil {
		log.Error(err, "marshal pod to json failed")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshal)
}

func (i *PodInjector) injectServiceAccountToken(ctx context.Context, pod *v1.Pod, sa *corev1alpha1.ServiceAccount) error {
	var tokenName string
	if len(sa.Secrets) == 0 {
		return fmt.Errorf("can not find any token in service account: %s", sa.Name)
	}

	// just select the first token, cause usually sa has only one token
	tokenName = sa.Secrets[0].Name

	secret := &v1.Secret{}
	err := i.Client.Get(ctx, types.NamespacedName{Namespace: sa.Namespace, Name: tokenName}, secret)
	if err != nil {
		return err
	}

	secretVolume := v1.Volume{
		Name: ServiceAccountVolumeName,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: tokenName,
				Items: []v1.KeyToPath{
					{
						Key:  corev1alpha1.ServiceAccountToken,
						Path: corev1alpha1.ServiceAccountToken,
					},
				},
			},
		},
	}

	volumeMount := v1.VolumeMount{
		Name:      ServiceAccountVolumeName,
		ReadOnly:  true,
		MountPath: VolumeMountPath,
	}

	if pod.Spec.Volumes == nil {
		volumes := make([]v1.Volume, 0)
		pod.Spec.Volumes = volumes
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, secretVolume)

	// mount to every container
	for i, c := range pod.Spec.Containers {
		volumeMounts := append(c.VolumeMounts, volumeMount)
		pod.Spec.Containers[i].VolumeMounts = volumeMounts
	}

	// mount to init container
	for i, c := range pod.Spec.InitContainers {
		volumeMounts := append(c.VolumeMounts, volumeMount)
		pod.Spec.InitContainers[i].VolumeMounts = volumeMounts
	}

	return nil
}
