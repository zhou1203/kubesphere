package marketplace

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"kubesphere.io/kubesphere/pkg/constants"

	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	marketplacev1alpha1 "kubesphere.io/api/marketplace/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/models/marketplace"
)

const (
	marketplaceController = "marketplace-controller"
	minPeriod             = time.Minute * 15
)

type Controller struct {
	client.Client
	recorder record.EventRecorder
	logger   logr.Logger
}

func (r *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithName(req.String())
	ctx = klog.NewContext(ctx, logger)

	extension := &corev1alpha1.Extension{}
	if err := r.Client.Get(ctx, req.NamespacedName, extension); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	options, err := marketplace.LoadOptions(ctx, r.Client)
	if err != nil {
		logger.Error(err, "failed to load marketplace configuration")
		return ctrl.Result{}, nil
	}

	if options.Account == nil {
		logger.V(4).Info("marketplace account not bound, skip synchronization")
		return ctrl.Result{}, nil
	}

	if extension.Labels[corev1alpha1.RepositoryReferenceLabel] == options.RepositorySyncOptions.RepoName {
		if extension.Labels[marketplacev1alpha1.ExtensionID] == "" {
			if err := r.syncExtensionID(ctx, options, extension); err != nil {
				return reconcile.Result{}, err
			}
		} else {
			if err := r.syncSubscription(ctx, options, extension); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	return reconcile.Result{}, nil
}

func (r *Controller) Start(ctx context.Context) error {
	options, err := marketplace.LoadOptions(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("failed to load marketplace configuration: %s", err)
	}
	if err := r.initMarketplaceRepository(ctx, options); err != nil {
		return err
	}
	go wait.Until(r.syncSubscriptions, max(options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
	return nil
}

func (r *Controller) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(marketplaceController)
	r.recorder = mgr.GetEventRecorderFor(marketplaceController)
	if err := mgr.Add(r); err != nil {
		return err
	}
	ctr, err := ctrl.NewControllerManagedBy(mgr).
		Named(marketplaceController).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetLabels()[corev1alpha1.RepositoryReferenceLabel] != "" &&
				object.GetLabels()[marketplacev1alpha1.ExtensionID] == ""
		})).
		For(&corev1alpha1.Extension{}).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", marketplaceController, err)
	}
	err = ctr.Watch(&source.Kind{Type: &corev1.Secret{}},
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			requests := make([]reconcile.Request, 0)
			secret := obj.(*corev1.Secret)
			options := &marketplace.Options{}
			if err := yaml.NewDecoder(bytes.NewReader(secret.Data[marketplace.ConfigurationFileKey])).Decode(options); err != nil {
				klog.Errorf("failed to decode marketplace configuration: %s", err)
				return requests
			}
			extensions := &corev1alpha1.ExtensionList{}
			if err := r.List(context.Background(), extensions, client.MatchingLabels{corev1alpha1.RepositoryReferenceLabel: options.RepositorySyncOptions.RepoName}); err != nil {
				klog.Errorf("failed to list extensions: %v", err)
				return requests
			}
			for _, extension := range extensions.Items {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: extension.Name,
					},
				})
			}
			return requests
		}),
		predicate.And(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if object.GetNamespace() == constants.KubeSphereNamespace && object.GetName() == marketplace.ConfigurationSecretName {
				return true
			}
			return false
		}), predicate.Funcs{
			GenericFunc: func(genericEvent event.GenericEvent) bool {
				return false
			},
			CreateFunc: func(event event.CreateEvent) bool {
				return false
			},
			UpdateFunc: func(updateEvent event.UpdateEvent) bool {
				secretOld := updateEvent.ObjectOld.(*corev1.Secret)
				secretNew := updateEvent.ObjectNew.(*corev1.Secret)

				dataOld := secretOld.Data[marketplace.ConfigurationFileKey]
				dataNew := secretNew.Data[marketplace.ConfigurationFileKey]

				optionsOld := &marketplace.Options{}
				if err := yaml.NewDecoder(bytes.NewReader(dataOld)).Decode(optionsOld); err != nil {
					return false
				}

				optionsNew := &marketplace.Options{}
				if err := yaml.NewDecoder(bytes.NewReader(dataNew)).Decode(optionsNew); err != nil {
					return false
				}

				tokenChanged := optionsNew.Account != nil && optionsNew.Account.AccessToken != "" &&
					(optionsOld.Account == nil || optionsOld.Account.AccessToken != optionsNew.Account.AccessToken)

				return tokenChanged
			},
			DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
				return false
			},
		}))

	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", marketplaceController, err)
	}

	return nil
}

func max(a time.Duration, b time.Duration) time.Duration {
	if a-b > 0 {
		return a
	} else {
		return b
	}
}

func (r *Controller) syncSubscriptions() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	options, err := marketplace.LoadOptions(ctx, r.Client)
	if err != nil {
		klog.Errorf("failed to sync subscriptions: %s", err)
		return
	}
	marketplaceClient := marketplace.NewClient(options)
	subscriptions, err := marketplaceClient.ListSubscriptions("")
	if err != nil {
		if marketplace.IsForbiddenError(err) {
			options.Account = nil
			if err := marketplace.SaveOptions(ctx, r.Client, options); err != nil {
				klog.Errorf("failed to save marketplace options: %s", err)
				return
			}
		}
		klog.Errorf("failed to sync subscriptions: %s", err)
		return
	}

	for _, subscription := range subscriptions {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			extensions := &corev1alpha1.ExtensionList{}
			if err := r.List(ctx, extensions,
				client.MatchingLabels{marketplacev1alpha1.ExtensionID: subscription.ExtensionID}); err != nil {
				return err
			}
			if len(extensions.Items) > 0 {
				extension := extensions.Items[0]
				if extension.Labels[marketplacev1alpha1.Subscribed] != "true" {
					extension.Labels[marketplacev1alpha1.Subscribed] = "true"
					if err := r.Update(ctx, &extension); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			klog.Errorf("failed to sync extension status: %s", err)
		}
	}
}

func (r *Controller) initMarketplaceRepository(ctx context.Context, options *marketplace.Options) error {
	repo := &corev1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: options.RepositorySyncOptions.RepoName}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
		repo.Spec = corev1alpha1.RepositorySpec{
			URL:         options.RepositorySyncOptions.URL,
			Description: "Builtin marketplace repository",
			UpdateStrategy: &corev1alpha1.UpdateStrategy{
				RegistryPoll: corev1alpha1.RegistryPoll{
					Interval: metav1.Duration{Duration: max(options.RepositorySyncOptions.SyncPeriod, minPeriod)},
				},
			},
		}
		if options.RepositorySyncOptions.BasicAuth != nil {
			repo.Spec.BasicAuth = &corev1alpha1.BasicAuth{
				Username: options.RepositorySyncOptions.BasicAuth.Username,
				Password: options.RepositorySyncOptions.BasicAuth.Password,
			}
		}
		return nil
	})
	return err
}

func (r *Controller) syncExtensionID(ctx context.Context, options *marketplace.Options, extension *corev1alpha1.Extension) error {
	id, err := marketplace.NewClient(options).ExtensionID(extension.Name)
	if err != nil {
		return err
	}
	if id != "" {
		extension = extension.DeepCopy()
		extension.Labels[marketplacev1alpha1.ExtensionID] = id
		return r.Update(ctx, extension, &client.UpdateOptions{})
	}
	return nil
}

func (r *Controller) syncSubscription(ctx context.Context, options *marketplace.Options, extension *corev1alpha1.Extension) error {
	subscriptions, err := marketplace.NewClient(options).ListSubscriptions(extension.Labels[marketplacev1alpha1.ExtensionID])
	if err != nil {
		return err
	}
	subscribed := len(subscriptions) > 0
	if subscribed && extension.Labels[marketplacev1alpha1.Subscribed] != "true" {
		extension = extension.DeepCopy()
		extension.Labels[marketplacev1alpha1.Subscribed] = "true"
		return r.Update(ctx, extension, &client.UpdateOptions{})
	} else if !subscribed && extension.Labels[marketplacev1alpha1.Subscribed] == "true" {
		extension = extension.DeepCopy()
		delete(extension.Labels, marketplacev1alpha1.Subscribed)
		return r.Update(ctx, extension, &client.UpdateOptions{})
	}
	return nil
}
