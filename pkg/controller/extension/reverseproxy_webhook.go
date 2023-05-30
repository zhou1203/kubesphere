package extension

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extensionsv1alpha1 "kubesphere.io/api/extensions/v1alpha1"
)

type ReverseProxyWebhook struct {
	client.Client
}

func (r *ReverseProxyWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	return r.validateReverseProxy(ctx, obj.(*extensionsv1alpha1.ReverseProxy))
}

func (r *ReverseProxyWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	return r.validateReverseProxy(ctx, newObj.(*extensionsv1alpha1.ReverseProxy))
}

func (r *ReverseProxyWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	return nil
}

func (r *ReverseProxyWebhook) validateReverseProxy(ctx context.Context, proxy *extensionsv1alpha1.ReverseProxy) error {
	reverseProxies := &extensionsv1alpha1.ReverseProxyList{}
	if err := r.Client.List(ctx, reverseProxies, &client.ListOptions{}); err != nil {
		return err
	}
	for _, reverseProxy := range reverseProxies.Items {
		if reverseProxy.Name == proxy.Name {
			continue
		}
		if reverseProxy.Spec.Matcher.Method != proxy.Spec.Matcher.Method &&
			reverseProxy.Spec.Matcher.Method != "*" {
			continue
		}
		if reverseProxy.Spec.Matcher.Path == proxy.Spec.Matcher.Path {
			return fmt.Errorf("ReverseProxy %v is already exists", proxy.Spec.Matcher)
		}
		if strings.HasSuffix(reverseProxy.Spec.Matcher.Path, "*") &&
			strings.HasPrefix(proxy.Spec.Matcher.Path, strings.TrimRight(reverseProxy.Spec.Matcher.Path, "*")) {
			return fmt.Errorf("ReverseProxy %v is already exists", proxy.Spec.Matcher)
		}
	}
	return nil
}

func (r *ReverseProxyWebhook) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(r).
		For(&extensionsv1alpha1.ReverseProxy{}).
		Complete()
}
