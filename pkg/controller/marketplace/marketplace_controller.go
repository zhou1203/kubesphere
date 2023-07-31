package marketplace

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
		logger.Error(err, "failed to load marketplace configuration")
		return ctrl.Result{}, nil
	}

	if extension.Labels[corev1alpha1.RepositoryReferenceLabel] != options.RepositorySyncOptions.RepoName {
		return ctrl.Result{}, nil
	}

	if extension.Labels[marketplacev1alpha1.ExtensionID] == "" {
		if err := r.syncExtensionID(ctx, options, extension); err != nil {
			return reconcile.Result{}, err
		}
	}

	hasCategoryLabels := false
	for k := range extension.Labels {
		if strings.HasPrefix(k, corev1alpha1.CategoryLabelPrefix+"/") {
			hasCategoryLabels = true
			break
		}
	}

	if !hasCategoryLabels && extension.Annotations[marketplacev1alpha1.CategoryID] != "" {
		if err := r.syncExtensionCategory(ctx, options, extension); err != nil {
			return reconcile.Result{}, err
		}
	}

	if options.Account != nil && options.Account.AccessToken != "" {
		if err := r.syncSubscription(ctx, options, extension); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		if err := marketplace.RemoveSubscription(ctx, r.Client, extension); err != nil {
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
	go wait.Until(r.syncSubscriptions, max(options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
	go wait.Until(r.syncCategories, max(options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
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
		For(&corev1alpha1.Extension{}).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", marketplaceController, err)
	}
	err = ctr.Watch(&source.Kind{Type: &corev1alpha1.Category{}},
		handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
			var requests []reconcile.Request
			category := object.(*corev1alpha1.Category)
			if category.Annotations[marketplacev1alpha1.CategoryID] == "" {
				return requests
			}
			extensions := &corev1alpha1.ExtensionList{}
			if err := r.List(context.Background(), extensions); err != nil {
				r.logger.Error(err, "failed to list extensions")
				return requests
			}
			for _, extension := range extensions.Items {
				if extension.Annotations[marketplacev1alpha1.CategoryID] == category.Annotations[marketplacev1alpha1.CategoryID] {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name: extension.Name,
						},
					})
				}
			}
			return requests
		}),
		predicate.ResourceVersionChangedPredicate{},
	)
	if err != nil {
		return fmt.Errorf("failed to setup %s: %s", marketplaceController, err)
	}
	err = ctr.Watch(&source.Kind{Type: &corev1.Secret{}},
		handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
			requests := make([]reconcile.Request, 0)
			secret := obj.(*corev1.Secret)
			options := &marketplace.Options{}
			if err := yaml.NewDecoder(bytes.NewReader(secret.Data[marketplace.ConfigurationFileKey])).Decode(options); err != nil {
				r.logger.Error(err, "failed to decode marketplace configuration")
				return requests
			}
			extensions := &corev1alpha1.ExtensionList{}
			if err := r.List(context.Background(), extensions, client.MatchingLabels{corev1alpha1.RepositoryReferenceLabel: options.RepositorySyncOptions.RepoName}); err != nil {
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

				return !reflect.DeepEqual(optionsNew.Account, optionsOld.Account)
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
		r.logger.Error(err, "failed to load marketplace configuration")
		return
	}

	if options.Account == nil || options.Account.AccessToken == "" {
		r.logger.V(4).Info("marketplace account not bound, skip synchronization")
		extensions := &corev1alpha1.ExtensionList{}
		if err := r.List(ctx, extensions,
			client.MatchingLabels{corev1alpha1.RepositoryReferenceLabel: options.RepositorySyncOptions.RepoName}); err != nil {
			for _, item := range extensions.Items {
				if err := marketplace.RemoveSubscription(ctx, r.Client, &item); err != nil {
					klog.Errorf("failed to update extension status: %s", err)
					continue
				}
			}
		}
		return
	}
	marketplaceClient := marketplace.NewClient(options)
	subscriptions, err := marketplaceClient.ListSubscriptions("")
	if err != nil {
		r.logger.Error(err, "failed to sync subscriptions")
		return
	}
	for _, subscription := range subscriptions {
		var extension *corev1alpha1.Extension
		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			extensions := &corev1alpha1.ExtensionList{}
			if err := r.List(ctx, extensions,
				client.MatchingLabels{marketplacev1alpha1.ExtensionID: subscription.ExtensionID}); err != nil {
				return err
			}
			if len(extensions.Items) > 0 {
				expected := &extensions.Items[0]
				if expected.Labels[marketplacev1alpha1.Subscribed] != "true" {
					expected.Labels[marketplacev1alpha1.Subscribed] = "true"
					if err := r.Update(ctx, expected); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			r.logger.Error(err, "failed to sync subscriptions")
			continue
		}
		if extension == nil {
			r.logger.Error(err, "subscription related extension not found", "subscription ID", subscription.SubscriptionID, "extension ID", subscription.ExtensionID)
			continue
		}
		if err = marketplace.CreateOrUpdateSubscription(ctx, r.Client, extension, subscription); err != nil {
			r.logger.Error(err, "failed to update subscription")
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
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		if expected.Labels == nil {
			extension.Labels = make(map[string]string, 0)
		}
		expected.Labels[marketplacev1alpha1.ExtensionID] = extensionInfo.ExtensionID
		if expected.Annotations == nil {
			expected.Annotations = make(map[string]string, 0)
		}
		expected.Annotations[marketplacev1alpha1.CategoryID] = extensionInfo.CategoryID
		if !reflect.DeepEqual(expected.Labels, extension.Labels) || !reflect.DeepEqual(expected.Annotations, extension.Annotations) {
			return r.Update(ctx, expected, &client.UpdateOptions{})
		}
		return nil
	})
}

func (r *Controller) syncSubscription(ctx context.Context, options *marketplace.Options, extension *corev1alpha1.Extension) error {
	subscriptions, err := marketplace.NewClient(options).ListSubscriptions(extension.Labels[marketplacev1alpha1.ExtensionID])
	if err != nil {
		return err
	}

	subscribed := len(subscriptions) > 0
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		if subscribed {
			expected.Labels[marketplacev1alpha1.Subscribed] = "true"
		} else if expected.Status.State == "" {
			delete(expected.Labels, marketplacev1alpha1.Subscribed)
		} else {
			expected.Labels[marketplacev1alpha1.Subscribed] = "false"
		}
		if !reflect.DeepEqual(expected.Labels, extension.Labels) {
			return r.Update(ctx, expected)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to update extension: %s", err)
	}

	if subscribed {
		for _, subscription := range subscriptions {
			if err := marketplace.CreateOrUpdateSubscription(ctx, r.Client, extension, subscription); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Controller) syncCategories() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
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
		r.createOrUpdateCategory(ctx, &category)
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
		category.Labels[marketplacev1alpha1.CategoryID] = origin.CategoryID
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

func (r *Controller) syncExtensionCategory(ctx context.Context, options *marketplace.Options, extension *corev1alpha1.Extension) error {
	categoryID := extension.Annotations[marketplacev1alpha1.CategoryID]
	categories := &corev1alpha1.CategoryList{}
	if err := r.List(ctx, categories, client.MatchingLabels{marketplacev1alpha1.CategoryID: categoryID}); err != nil {
		return fmt.Errorf("failed to list categories: %s", err)
	}

	category := &corev1alpha1.Category{}
	if len(categories.Items) > 0 {
		category = &categories.Items[0]
	} else {
		categoryInfo, err := marketplace.NewClient(options).CategoryInfo(categoryID)
		if err != nil {
			return fmt.Errorf("failed to describe category %s: %s", categoryID, err)
		}
		if err := r.createOrUpdateCategory(ctx, categoryInfo); err != nil {
			return fmt.Errorf("failed to update category: %s", err)
		}
		if err := r.Get(ctx, types.NamespacedName{Name: categoryInfo.NormalizedName}, category); err != nil {
			return fmt.Errorf("failed to describe category %s: %s", categoryInfo.NormalizedName, err)
		}
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		expected.Labels[fmt.Sprintf(corev1alpha1.CategoryLabelFormat, category.Name)] = ""
		return r.Update(ctx, expected)
	})
}
