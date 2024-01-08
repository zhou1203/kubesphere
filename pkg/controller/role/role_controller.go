package role

import (
	"context"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/record"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	rbachelper "kubesphere.io/kubesphere/pkg/componenthelper/auth/rbac"
)

const controllerName = "role"

var _ kscontroller.Controller = &Reconciler{}

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
	r.Client = mgr.GetClient()
	r.logger = ctrl.Log.WithName("controllers").WithName(controllerName)
	r.recorder = mgr.GetEventRecorderFor(controllerName)
	r.helper = rbachelper.NewHelper(r.Client)
	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		For(&iamv1beta1.Role{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	role := &iamv1beta1.Role{}
	if err := r.Get(ctx, req.NamespacedName, role); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if role.AggregationRoleTemplates != nil {
		if err := r.helper.AggregationRole(ctx, rbachelper.RoleRuleOwner{Role: role}, r.recorder); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}
