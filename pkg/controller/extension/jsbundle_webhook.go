package extension

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"
)

type JSBundleWebhook struct {
	client.Client
}

var _ admission.CustomDefaulter = &JSBundleWebhook{}

func (r *JSBundleWebhook) Default(ctx context.Context, obj runtime.Object) error {
	jsBundle := obj.(*extensionsv1alpha1.JSBundle)
	if jsBundle.Status.Link == "" {
		jsBundle.Status.Link = fmt.Sprintf("/dist/%s/index.js", jsBundle.Name)
	}
	return nil
}

func (r *JSBundleWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	return r.validateJSBundle(ctx, obj.(*extensionsv1alpha1.JSBundle))
}

func (r *JSBundleWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	return r.validateJSBundle(ctx, newObj.(*extensionsv1alpha1.JSBundle))
}

func (r *JSBundleWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (r *JSBundleWebhook) validateJSBundle(ctx context.Context, bundle *extensionsv1alpha1.JSBundle) error {
	if bundle.Status.Link == "" {
		return nil
	}
	if !strings.HasPrefix(bundle.Status.Link, fmt.Sprintf("/dist/%s", bundle.Name)) {
		return fmt.Errorf("the prefix of status.link must be in the format /dist/%s/", bundle.Name)
	}
	jsBundles := &extensionsv1alpha1.JSBundleList{}
	if err := r.Client.List(ctx, jsBundles, &client.ListOptions{}); err != nil {
		return err
	}
	for _, jsBundle := range jsBundles.Items {
		if jsBundle.Name != bundle.Name &&
			jsBundle.Status.Link == bundle.Status.Link {
			return fmt.Errorf("JSBundle %s is already exists", bundle.Status.Link)
		}
	}
	return nil
}

func (r *JSBundleWebhook) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		WithDefaulter(r).
		For(&extensionsv1alpha1.JSBundle{}).
		Complete()
}
