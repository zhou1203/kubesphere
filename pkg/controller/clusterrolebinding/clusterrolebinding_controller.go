package clusterrolebinding

import (
	"context"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	rbachelper "kubesphere.io/kubesphere/pkg/componenthelper/auth/rbac"
)

const controllerName = "clusterrolebinding"

var _ kscontroller.Controller = &Reconciler{}
var _ reconcile.Reconciler = &Reconciler{}

type Reconciler struct {
	client.Client
	logger   logr.Logger
	recorder record.EventRecorder
	helper   *rbachelper.Helper
}

func (r *Reconciler) Name() string {
	return controllerName
}

func (r *Reconciler) SetupWithManager(mgr *kscontroller.Manager) error {
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.Client = mgr.GetClient()
	r.helper = rbachelper.NewHelper(mgr.GetClient())

	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		For(&iamv1beta1.ClusterRoleBinding{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	clusterRole := &iamv1beta1.ClusterRoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, clusterRole); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}
