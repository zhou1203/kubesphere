package extension

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"
)

type APIServiceWebhook struct {
	client.Client
}

func (r *APIServiceWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	return r.validateAPIService(ctx, obj.(*extensionsv1alpha1.APIService))
}

func (r *APIServiceWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	return r.validateAPIService(ctx, newObj.(*extensionsv1alpha1.APIService))
}

func (r *APIServiceWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (r *APIServiceWebhook) validateAPIService(ctx context.Context, service *extensionsv1alpha1.APIService) error {
	apiServices := &extensionsv1alpha1.APIServiceList{}
	if err := r.Client.List(ctx, apiServices, &client.ListOptions{}); err != nil {
		return err
	}
	for _, apiService := range apiServices.Items {
		if apiService.Name != service.Name &&
			apiService.Spec.Group == service.Spec.Group &&
			apiService.Spec.Version == service.Spec.Version {
			return fmt.Errorf("APIService %s/%s is already exists", service.Spec.Group, service.Spec.Version)
		}
	}
	return nil
}

func (r *APIServiceWebhook) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&extensionsv1alpha1.APIService{}).
		Complete()
}
