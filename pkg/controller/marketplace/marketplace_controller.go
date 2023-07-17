package marketplace

import (
	"context"
	"time"

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
	recorder    record.EventRecorder
	logger      logr.Logger
	ctx         context.Context
	options     *marketplace.Options
	marketplace marketplace.Interface
}

func (r *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithName(req.String())
	ctx = klog.NewContext(ctx, logger)

	extension := &corev1alpha1.Extension{}
	if err := r.Client.Get(ctx, req.NamespacedName, extension); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if extension.Labels[corev1alpha1.RepositoryReferenceLabel] == r.options.RepositorySyncOptions.RepoName &&
		extension.Labels[marketplacev1alpha1.ExtensionID] == "" {
		if err := r.syncExtensionID(ctx, extension); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *Controller) Start(ctx context.Context) error {
	options, err := marketplace.LoadOptions(context.Background(), r.Client)
	if err != nil {
		return err
	}
	r.options = options
	r.marketplace = marketplace.NewClient(options)
	if err := r.initMarketplaceRepository(ctx); err != nil {
		return err
	}
	r.ctx = ctx
	go wait.Until(r.syncSubscriptions, max(r.options.SubscriptionSyncOptions.SyncPeriod, minPeriod), ctx.Done())
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
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if r.options == nil {
				return false
			}
			return object.GetLabels()[corev1alpha1.RepositoryReferenceLabel] == r.options.RepositorySyncOptions.RepoName &&
				object.GetLabels()[marketplacev1alpha1.ExtensionID] == ""
		})).
		For(&corev1alpha1.Extension{}).
		Complete(r)
}

func max(a time.Duration, b time.Duration) time.Duration {
	if a-b > 0 {
		return a
	} else {
		return b
	}
}

func (r *Controller) syncSubscriptions() {
	options, err := marketplace.LoadOptions(r.ctx, r.Client)
	if err != nil {
		klog.Errorf("failed to sync subscriptions: %s", err)
		return
	}

	marketplaceClient := marketplace.NewClient(options)
	subscriptions, err := marketplaceClient.ListSubscriptions()
	if err != nil {
		if marketplace.IsForbiddenError(err) {
			options.Account = nil
			if err := marketplace.SaveOptions(r.ctx, r.Client, options); err != nil {
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
			if err := r.List(r.ctx, extensions,
				client.MatchingLabels{marketplacev1alpha1.ExtensionID: subscription.ExtensionID}); err != nil {
				return err
			}
			if len(extensions.Items) > 0 {
				extension := extensions.Items[0]
				if extension.Labels[marketplacev1alpha1.Subscribed] != "true" {
					extension.Labels[marketplacev1alpha1.Subscribed] = "true"
					if err := r.Update(r.ctx, &extension); err != nil {
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

func (r *Controller) initMarketplaceRepository(ctx context.Context) error {
	repo := &corev1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: r.options.RepositorySyncOptions.RepoName}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, repo, func() error {
		repo.Spec = corev1alpha1.RepositorySpec{
			URL:         r.options.RepositorySyncOptions.URL,
			Description: "Builtin marketplace repository",
			UpdateStrategy: &corev1alpha1.UpdateStrategy{
				RegistryPoll: corev1alpha1.RegistryPoll{
					Interval: metav1.Duration{Duration: max(r.options.RepositorySyncOptions.SyncPeriod, minPeriod)},
				},
			},
		}
		if r.options.RepositorySyncOptions.BasicAuth != nil {
			repo.Spec.BasicAuth = &corev1alpha1.BasicAuth{
				Username: r.options.RepositorySyncOptions.BasicAuth.Username,
				Password: r.options.RepositorySyncOptions.BasicAuth.Password,
			}
		}
		return nil
	})
	return err
}

func (r *Controller) syncExtensionID(ctx context.Context, extension *corev1alpha1.Extension) error {
	id, err := r.marketplace.ExtensionID(extension.Name)
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
