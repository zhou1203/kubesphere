package marketplace

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	marketplacev1alpha1 "kubesphere.io/api/marketplace/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/constants"
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if extension.Labels[corev1alpha1.RepositoryReferenceLabel] != options.RepositorySyncOptions.RepoName {
		return ctrl.Result{}, nil
	}

	if extension.Labels[marketplacev1alpha1.ExtensionID] == "" {
		if err := r.syncExtensionID(ctx, options, extension); err != nil {
			return reconcile.Result{}, err
		}
	}

	if extension.Labels[marketplacev1alpha1.ExtensionID] == "" {
		r.logger.V(4).Info("extension ID not exists")
		return reconcile.Result{}, nil
	}

	if options.Account.IsValid() {
		if err := r.syncSubscription(ctx, options, extension); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		if err := marketplace.RemoveSubscriptions(ctx, r.Client, extension); err != nil {
			return reconcile.Result{}, err
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
	go wait.Until(r.syncSubscriptions(ctx), max(options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
	go wait.Until(r.syncCategories(ctx), max(options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
	return nil
}

func (r *Controller) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(marketplaceController)
	r.recorder = mgr.GetEventRecorderFor(marketplaceController)
	if err := mgr.Add(r); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named(marketplaceController).
		For(&corev1alpha1.Extension{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				requests := make([]reconcile.Request, 0)
				secret := obj.(*corev1.Secret)
				options := &marketplace.Options{}
				if err := yaml.NewDecoder(bytes.NewReader(secret.Data[marketplace.ConfigurationFileKey])).Decode(options); err != nil {
					r.logger.Error(err, "failed to decode marketplace configuration")
					return requests
				}
				extensions := &corev1alpha1.ExtensionList{}
				if err := r.List(ctx, extensions, client.MatchingLabels{corev1alpha1.RepositoryReferenceLabel: options.RepositorySyncOptions.RepoName}); err != nil {
					r.logger.Error(err, "failed to list extensions")
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
			builder.WithPredicates(predicate.And(predicate.NewPredicateFuncs(func(object client.Object) bool {
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

					return !reflect.DeepEqual(optionsNew.Account, optionsOld.Account)
				},
				DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
					return false
				},
			})),
		).
		Complete(r)
}

func max(a time.Duration, b time.Duration) time.Duration {
	if a-b > 0 {
		return a
	} else {
		return b
	}
}

func (r *Controller) syncSubscriptions(ctx context.Context) func() {
	return func() {
		err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return true
		}, func() error {
			options, err := marketplace.LoadOptions(ctx, r.Client)
			if err != nil {
				return fmt.Errorf("failed to load marketplace configuration: %s", err)
			}

			if !options.Account.IsValid() {
				r.logger.V(4).Info("marketplace account not bound")
				if err := r.cleanup(ctx); err != nil {
					return fmt.Errorf("failed cleanup marketplace account related resources: %s", err)
				}
				return nil
			}

			marketplaceClient := marketplace.NewClient(options)
			subscriptions, err := marketplaceClient.ListSubscriptions("")
			if err != nil {
				return fmt.Errorf("failed to fetch subscriptions: %s", err)
			}
			for _, subscription := range subscriptions {
				extensions := &corev1alpha1.ExtensionList{}
				if err := r.List(ctx, extensions,
					client.MatchingLabels{marketplacev1alpha1.ExtensionID: subscription.ExtensionID}); err != nil {
					return fmt.Errorf("failed to list extensions: %s", err)
				}

				if len(extensions.Items) == 0 {
					r.logger.Info("subscribed extension not found", "subscription ID", subscription.SubscriptionID, "extension ID", subscription.ExtensionID)
					continue
				}

				if len(extensions.Items) > 1 {
					r.logger.Info("extension conflict found", "extension ID", subscription.SubscriptionID)
					continue
				}

				extension := extensions.Items[0]
				if err = marketplace.CreateOrUpdateSubscription(ctx, r.Client, extension.Name, subscription); err != nil {
					return fmt.Errorf("failed to update subscription: %s", err)
				}
				if extension.Labels[marketplacev1alpha1.Subscribed] != "true" {
					extension.Labels[marketplacev1alpha1.Subscribed] = "true"
					if err := r.Update(ctx, &extension); err != nil {
						return fmt.Errorf("failed to update extension %s: %s", extension.Name, err)
					}
				}
			}

			storedSubscriptions := &marketplacev1alpha1.SubscriptionList{}
			if err := r.List(ctx, storedSubscriptions, client.MatchingLabels{}); err != nil {
				return fmt.Errorf("failed to list subscriptions: %s", err)
			}

			outOfDateSubscriptions := make([]*marketplacev1alpha1.Subscription, 0)
			for _, sub := range storedSubscriptions.Items {
				if !slices.ContainsFunc(subscriptions, func(subscription marketplace.Subscription) bool {
					return sub.SubscriptionStatus.SubscriptionID == subscription.SubscriptionID
				}) {
					outOfDateSubscriptions = append(outOfDateSubscriptions, &sub)
				}
			}

			for _, subscription := range outOfDateSubscriptions {
				r.logger.V(4).Info("delete out of date subscription", "subscription", subscription.Name)
				if err := r.Client.Delete(ctx, subscription); err != nil {
					return fmt.Errorf("failed to delete subscription %s: %s", subscription.Name, err)
				}
			}

			return nil
		})
		if err != nil {
			r.logger.Error(err, "failed to sync subscriptions")
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
	extensionInfo, err := marketplace.NewClient(options).ExtensionInfo(extension.Name)
	if err != nil {
		return err
	}
	if extensionInfo.ExtensionID == "" {
		r.logger.V(4).Info("extensionID not exists", "extension", extension.Name)
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		if extension.Labels == nil {
			extension.Labels = make(map[string]string, 0)
		}
		extension.Labels[marketplacev1alpha1.ExtensionID] = extensionInfo.ExtensionID
		if !reflect.DeepEqual(expected.Labels, extension.Labels) {
			return r.Update(ctx, extension, &client.UpdateOptions{})
		}
		return nil
	})
}

func (r *Controller) syncSubscription(ctx context.Context, options *marketplace.Options, extension *corev1alpha1.Extension) error {
	extensionID := extension.Labels[marketplacev1alpha1.ExtensionID]
	subscriptions, err := marketplace.NewClient(options).ListSubscriptions(extensionID)
	if err != nil {
		return err
	}

	subscribed := len(subscriptions) > 0
	for _, subscription := range subscriptions {
		if err := marketplace.CreateOrUpdateSubscription(ctx, r.Client, extension.Name, subscription); err != nil {
			return err
		}
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return fmt.Errorf("failed to get extension details: %s", err)
		}
		expected := extension.DeepCopy()
		if subscribed {
			expected.Labels[marketplacev1alpha1.Subscribed] = "true"
		} else {
			delete(expected.Labels, marketplacev1alpha1.Subscribed)
		}
		if !reflect.DeepEqual(expected.Labels, extension.Labels) {
			if err := r.Update(ctx, expected); err != nil {
				return fmt.Errorf("failed to update extension %s: %s", expected.Name, err)
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	storedSubscriptions := &marketplacev1alpha1.SubscriptionList{}
	if err := r.List(ctx, storedSubscriptions, client.MatchingLabels{marketplacev1alpha1.ExtensionID: extensionID}); err != nil {
		return fmt.Errorf("failed to list subscriptions: %s", err)
	}

	outOfDateSubscriptions := make([]*marketplacev1alpha1.Subscription, 0)
	for _, sub := range storedSubscriptions.Items {
		if !slices.ContainsFunc(subscriptions, func(subscription marketplace.Subscription) bool {
			return sub.SubscriptionStatus.SubscriptionID == subscription.SubscriptionID
		}) {
			outOfDateSubscriptions = append(outOfDateSubscriptions, &sub)
		}
	}

	for _, subscription := range outOfDateSubscriptions {
		r.logger.V(4).Info("delete out of date subscription", "subscription", subscription.Name)
		if err := r.Delete(ctx, subscription); err != nil {
			return fmt.Errorf("failed to delete subscription %s: %s", subscription.Name, err)
		}
	}
	return nil
}

func (r *Controller) syncCategories(ctx context.Context) func() {
	return func() {
		options, err := marketplace.LoadOptions(context.Background(), r.Client)
		if err != nil {
			r.logger.Error(err, "failed to sync categories")
			return
		}
		categories, err := marketplace.NewClient(options).ListCategories()
		if err != nil {
			r.logger.Error(err, "failed to list categories")
		}
		for _, category := range categories {
			if err := r.createOrUpdateCategory(ctx, &category); err != nil {
				klog.Errorf("failed to update category: %s", err)
				continue
			}
		}
	}
}

func (r *Controller) createOrUpdateCategory(ctx context.Context, origin *marketplace.Category) error {
	category := &corev1alpha1.Category{
		ObjectMeta: metav1.ObjectMeta{Name: origin.NormalizedName},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, category, func() error {
		if category.Labels == nil {
			category.Labels = make(map[string]string)
		}
		category.Spec = corev1alpha1.CategorySpec{
			DisplayName: origin.Name,
			Icon:        "",
		}
		return nil
	})
	if err != nil {
		return err
	}
	klog.V(4).Infof("Update category %s successfully: %s", origin.NormalizedName, op)
	return nil
}

func (r *Controller) cleanup(ctx context.Context) error {
	if err := r.DeleteAllOf(ctx, &marketplacev1alpha1.Subscription{}); err != nil {
		return fmt.Errorf("failed to delete subscriptions: %s", err)
	}

	extensions := &corev1alpha1.ExtensionList{}
	if err := r.List(ctx, extensions,
		client.MatchingLabels{marketplacev1alpha1.Subscribed: "true"}); err != nil {
		return fmt.Errorf("failed to list extensions: %s", err)
	}

	for _, extension := range extensions.Items {
		delete(extension.Labels, marketplacev1alpha1.Subscribed)
		if err := r.Update(ctx, &extension); err != nil {
			return fmt.Errorf("failed to update extension %s: %s", extension.Name, err)
		}
	}
	return nil
}
