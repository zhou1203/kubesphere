package applicationclass

import (
	"context"
	"os"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"kubesphere.io/api/applicationclass/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	applicationController = "application-controller"
	applicationFinalizer  = "applications.kubesphere.io"
)

var _ reconcile.Reconciler = &ApplicationReconciler{}

type ApplicationReconciler struct {
	client.Client
	kubeconfig           string
	recorder             record.EventRecorder
	logger               logr.Logger
	ApplicationPluginMgr ApplicationPluginMgr
}

func NewApplicationReconciler(kubeconfigPath string) (*ApplicationReconciler, error) {
	var kubeconfig string
	if kubeconfigPath != "" {
		data, err := os.ReadFile(kubeconfigPath)
		if err != nil {
			return nil, err
		}
		kubeconfig = string(data)
	}
	return &ApplicationReconciler{kubeconfig: kubeconfig}, nil
}

func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(applicationController)
	r.recorder = mgr.GetEventRecorderFor(applicationController)
	return ctrl.NewControllerManagedBy(mgr).
		Named(applicationController).
		For(&v1alpha1.Application{}).Complete(r)
}

func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.logger.WithValues("application", req.String())
	logger.V(4).Info("sync application")

	app := &v1alpha1.Application{}
	if err := r.Client.Get(ctx, req.NamespacedName, app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctx = klog.NewContext(ctx, logger)

	if !controllerutil.ContainsFinalizer(app, applicationFinalizer) {
		expected := app.DeepCopy()
		controllerutil.AddFinalizer(expected, applicationFinalizer)
		return ctrl.Result{}, r.Patch(ctx, expected, client.MergeFrom(app))
	}

	if !app.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, app)
	}

	if err := r.syncApplication(ctx, app); err != nil {
		logger.Error(err, "failed to sync application status")
		return ctrl.Result{}, err
	}

	r.logger.V(4).Info("successfully synced")
	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) syncApplication(ctx context.Context, app *v1alpha1.Application) error {
	if !metav1.HasAnnotation(app.ObjectMeta, ApplicationReleased) {
		return r.syncUnboundApplication(ctx, app)
	} else {
		return r.syncBoundApplication(app)
	}
}

func (r *ApplicationReconciler) syncUnboundApplication(ctx context.Context, app *v1alpha1.Application) error {
	if err := r.provisionApp(ctx, app); err != nil {
		return err
	}
	//r.updateApplication()
	return nil
}

func (r *ApplicationReconciler) syncBoundApplication(app *v1alpha1.Application) error {
	// TODO
	return nil
}

func (r *ApplicationReconciler) provisionApp(ctx context.Context, app *v1alpha1.Application) error {
	logger := klog.FromContext(ctx)
	plugin, applicationClass, err := r.findProvisionablePlugin(app)
	if err != nil {
		logger.Error(err, "provisionApp failed to find provisioning plugin for application")
		return err
	}

	options := ApplicationOptions{
		Client:          r.Client,
		Kubeconfig:      r.kubeconfig,
		AppResourceName: app.Name,
		App:             app,
		Parameters:      applicationClass.Parameters,
	}
	provisioner, err := plugin.NewProvisioner(options)
	if err != nil {
		logger.Error(err, "provisionApp failed to create provisioner")
		return err
	}

	_, err = provisioner.Provision(ctx)
	if err != nil {
		logger.Error(err, "failed to provision application resource")
		//r.updateApplication()
		return err
	}

	return nil
}

func (r *ApplicationReconciler) findProvisionablePlugin(app *v1alpha1.Application) (ProvisionAppResourcePlugin, *v1alpha1.ApplicationClass, error) {

	appClass := &v1alpha1.ApplicationClass{}
	appClassKey := types.NamespacedName{
		Name: app.Spec.ApplicationClassName,
	}
	err := r.Client.Get(context.Background(), appClassKey, appClass)
	if err != nil {
		return nil, nil, err
	}

	plugin, err := r.ApplicationPluginMgr.FindProvisionablePluginByName(appClass.Provisioner)
	if err != nil {
		return nil, appClass, err
	}

	return plugin, appClass, nil
}

func (r *ApplicationReconciler) reconcileDelete(ctx context.Context, app *v1alpha1.Application) (ctrl.Result, error) {

	if app.Status.ResourceName == "" {
		if err := r.postRemove(ctx, app); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.uninstallApp(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ApplicationReconciler) uninstallApp(ctx context.Context, app *v1alpha1.Application) error {
	logger := klog.FromContext(ctx)
	plugin, applicationClass, err := r.findProvisionablePlugin(app)
	if err != nil {
		logger.Error(err, "uninstallApp failed to find provisioning plugin for application")
		return err
	}

	options := ApplicationOptions{
		Client:     r.Client,
		Kubeconfig: r.kubeconfig,
		App:        app,
		Parameters: applicationClass.Parameters,
	}
	provisioner, err := plugin.NewProvisioner(options)
	if err != nil {
		logger.Error(err, "provisionApp failed to create provisioner")
		return err
	}

	if app.Status.State != v1alpha1.StateUninstalling {
		err = provisioner.Uninstall(ctx)
		if err != nil {
			logger.Error(err, "failed to uninstall application resource")
			if err := r.updateApplication(ctx, app); err != nil {
				return err
			}
			return nil
		}
	}

	return nil
}

func (r *ApplicationReconciler) postRemove(ctx context.Context, app *v1alpha1.Application) error {
	// Remove the finalizer
	controllerutil.RemoveFinalizer(app, applicationFinalizer)
	app.Status.State = v1alpha1.StateUninstalled
	return r.updateApplication(ctx, app)
}

func (r *ApplicationReconciler) updateApplication(ctx context.Context, app *v1alpha1.Application) error {
	logger := klog.FromContext(ctx)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		newApp := &v1alpha1.Application{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: app.Name}, newApp); err != nil {
			return err
		}
		newApp.Finalizers = app.Finalizers
		newApp.Annotations = app.Annotations
		newApp.Status = app.Status
		return r.Update(ctx, newApp)
	}); err != nil {
		logger.Error(err, "failed to update application")
		return err
	}
	return nil
}
