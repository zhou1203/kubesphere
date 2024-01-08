package rolebinding

import (
	"context"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const controllerName = "rolebinding-controller"

var _ kscontroller.Controller = &Reconciler{}

type Reconciler struct {
	client.Client
	logger   logr.Logger
	recorder record.EventRecorder
}

func (r *Reconciler) Name() string {
	return controllerName
}

func (r *Reconciler) SetupWithManager(mgr *kscontroller.Manager) error {
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		For(&iamv1beta1.RoleBinding{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	role := &iamv1beta1.RoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, role); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}
