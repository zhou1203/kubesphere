package controller

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"

	idp "kubesphere.io/kubesphere/pkg/apiserver/authentication/identityprovider"
	"kubesphere.io/kubesphere/pkg/constants"
)

var identityProviderHandler idp.Handler

func SetupIdentityProvider(informer runtimecache.Informer, idpHandler idp.Handler, stopCh <-chan struct{}) {

	identityProviderHandler = idpHandler

	go func() {
		cache.WaitForCacheSync(stopCh, informer.HasSynced)
		_, err := informer.AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				secret := obj.(*v1.Secret)
				return isProviderSecret(secret)
			},
			Handler: &cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					secret := obj.(*v1.Secret)
					createOrUpdateProvider(secret)
				},
				UpdateFunc: func(old, new interface{}) {
					secret := new.(*v1.Secret)
					createOrUpdateProvider(secret)
				},
				DeleteFunc: func(obj interface{}) {
					secret := obj.(*v1.Secret)
					deleteProvider(secret)
				},
			},
		})
		if err != nil {
			klog.Errorf("add informer eventHandler failed: %s", err.Error())
		}
	}()
}

func handleError(providerName string, err error) {
	if err != nil {
		klog.ErrorDepth(2, fmt.Errorf("sync identity provider %s failed, %s", providerName, err.Error()))
	}
}

func isProviderSecret(secret *v1.Secret) bool {
	if secret.Namespace != constants.KubeSphereNamespace {
		return false
	}
	return secret.Type == idp.SecretTypeIdentityProvider
}

func deleteProvider(secret *v1.Secret) {
	var err error

	idpName := secret.Labels[idp.SecretLabelIdentityProviderName]
	idpCategory := secret.Labels[idp.AnnotationProviderCategory]

	var errNotExist = fmt.Errorf("identity provider not exist")

	switch idpCategory {
	case idp.CategoryGeneric:
		_, exist := identityProviderHandler.GetOAuthProvider(idpName)
		if !exist {
			err = errNotExist
			handleError(idpName, err)
			return
		}
		identityProviderHandler.DeleteGenericProvider(idpName)
	case idp.CategoryOAuth:
		_, exist := identityProviderHandler.GetOAuthProvider(idpName)
		if !exist {
			err = errNotExist
			handleError(idpName, err)
			return
		}
		identityProviderHandler.DeleteOAuthProvider(idpName)
	default:
		handleError(idpName, fmt.Errorf("not supported identity provider category: %s", idpCategory))
	}
}

func createOrUpdateProvider(secret *v1.Secret) {
	var err error
	idpName := secret.Labels[idp.SecretLabelIdentityProviderName]
	idpCategory := secret.Annotations[idp.AnnotationProviderCategory]
	configuration, err := idp.UnmarshalTo(*secret)
	if err != nil {
		handleError(idpName, err)
		return
	}

	switch idpCategory {
	case idp.CategoryOAuth:
		err = identityProviderHandler.RegisterOAuthProvider(configuration)
	case idp.CategoryGeneric:
		err = identityProviderHandler.RegisterGenericProvider(configuration)
	default:
		err = fmt.Errorf("not supported identity provider category: %s", idpCategory)
	}
	handleError(idpName, err)
}
