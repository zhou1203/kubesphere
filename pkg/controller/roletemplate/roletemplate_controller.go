package roletemplate

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	rbachelper "kubesphere.io/kubesphere/pkg/conponenthelper/auth/rbac"
	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	autoAggregateIndexKey = ".metadata.annotations[iam.kubesphere.io/auto-aggregate]"
	autoAggregationLabel  = "iam.kubesphere.io/auto-aggregate"
	controllerName        = "roletemplate-controller"
	reasonFailedSync      = "FailedInjectRoleTemplate"
	messageResourceSynced = "RoleTemplate injected successfully"
)

// Reconciler reconciles a RoleTemplate object
type Reconciler struct {
	client.Client
	recorder record.EventRecorder
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	roletemplate := &iamv1beta1.RoleTemplate{}
	err := r.Client.Get(ctx, req.NamespacedName, roletemplate)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err = r.injectRoleTemplateToRuleOwner(ctx, roletemplate)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) injectRoleTemplateToRuleOwner(ctx context.Context, roletemplate *iamv1beta1.RoleTemplate) error {
	if _, exist := roletemplate.Labels[rbachelper.LabelGlobalScope]; exist {
		if err := r.aggregateGlobalRoles(ctx, roletemplate); err != nil {
			return err
		}
	}
	if _, exist := roletemplate.Labels[rbachelper.LabelWorkspaceScope]; exist {
		if err := r.aggregateWorkspaceRoles(ctx, roletemplate); err != nil {
			return err
		}
	}
	if _, exist := roletemplate.Labels[rbachelper.LabelClusterScope]; exist {
		if err := r.aggregateClusterRoles(ctx, roletemplate); err != nil {
			return err
		}
	}
	if _, exist := roletemplate.Labels[rbachelper.LabelNamespaceScope]; exist {
		if err := r.aggregateRoles(ctx, roletemplate); err != nil {
			return err
		}
	}
	return nil
}

// aggregateGlobalRoles automatic inject the RoleTemplate`s rules to the role
// (all role types have the field AggregationRoleTemplates) matching the AggregationRoleTemplates filed.
// Note that autoAggregateRoles just aggregate the templates by field ".aggregationRoleTemplates.roleSelectors",
// and if the roleTemplate content is changed, the role including the roleTemplate should not be updated.
func (r *Reconciler) aggregateGlobalRoles(ctx context.Context, roletemplate *iamv1beta1.RoleTemplate) error {
	list := &iamv1beta1.GlobalRoleList{}
	l := map[string]string{autoAggregateIndexKey: "true"}
	err := r.List(ctx, list, client.MatchingFields(l))
	if err != nil {
		return err
	}

	for _, role := range list.Items {
		err := r.aggregate(ctx, rbachelper.GlobalRoleRuleOwner{GlobalRole: &role}, roletemplate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) aggregateWorkspaceRoles(ctx context.Context, roletemplate *iamv1beta1.RoleTemplate) error {
	list := &iamv1beta1.WorkspaceRoleList{}
	l := map[string]string{autoAggregateIndexKey: "true"}
	err := r.List(ctx, list, client.MatchingFields(l))
	if err != nil {
		return err
	}

	for _, role := range list.Items {
		err := r.aggregate(ctx, rbachelper.WorkspaceRoleRuleOwner{WorkspaceRole: &role}, roletemplate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) aggregateClusterRoles(ctx context.Context, roletemplate *iamv1beta1.RoleTemplate) error {
	list := &iamv1beta1.ClusterRoleList{}
	if err := r.List(ctx, list, client.MatchingFields(map[string]string{autoAggregateIndexKey: "true"})); err != nil {
		return err
	}

	for _, role := range list.Items {
		err := r.aggregate(ctx, rbachelper.ClusterRoleRuleOwner{ClusterRole: &role}, roletemplate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) aggregateRoles(ctx context.Context, roletemplate *iamv1beta1.RoleTemplate) error {
	list := &iamv1beta1.RoleList{}
	l := map[string]string{autoAggregateIndexKey: "true"}
	err := r.List(ctx, list, client.MatchingFields(l))
	if err != nil {
		return err
	}

	for _, role := range list.Items {
		err := r.aggregate(ctx, rbachelper.RoleRuleOwner{Role: &role}, roletemplate)
		if err != nil {
			return err
		}
	}
	return nil
}

// aggregate the role-template rules to the ruleOwner. If the role-template is updated but has already been aggregated by the ruleOwner,
// the ruleOwner cannot update the new role-template rule to the ruleOwner.
func (r *Reconciler) aggregate(ctx context.Context, ruleOwner rbachelper.RuleOwner, roleTemplate *iamv1beta1.RoleTemplate) error {
	aggregation := ruleOwner.GetAggregationRule()
	if aggregation == nil {
		return nil
	}

	hasTemplateName := sliceutil.HasString(aggregation.TemplateNames, roleTemplate.Name)
	if !hasTemplateName {
		if isContainsLabels(roleTemplate.Labels, aggregation.RoleSelector.MatchLabels) {
			cover, _ := rbachelper.Covers(ruleOwner.GetRules(), roleTemplate.Spec.Rules)
			if cover && hasTemplateName {
				return nil
			}

			if !cover {
				ruleOwner.SetRules(append(ruleOwner.GetRules(), roleTemplate.Spec.Rules...))
			}

			if !hasTemplateName {
				aggregation.TemplateNames = append(aggregation.TemplateNames, roleTemplate.Name)
				ruleOwner.SetAggregationRule(aggregation)
			}

			if err := r.Client.Update(ctx, ruleOwner.GetObject().(client.Object)); err != nil {
				r.recorder.Event(ruleOwner.GetObject(), corev1.EventTypeWarning, reasonFailedSync, err.Error())
				return err
			}

			r.recorder.Event(ruleOwner.GetObject(), corev1.EventTypeNormal, "Synced", messageResourceSynced)
		}
	}

	return nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {

	r.recorder = mgr.GetEventRecorderFor(controllerName)

	if err := mgr.GetCache().IndexField(context.Background(), &iamv1beta1.GlobalRole{}, autoAggregateIndexKey, globalRoleIndexByAnnotation); err != nil {
		return err
	}
	if err := mgr.GetCache().IndexField(context.Background(), &iamv1beta1.WorkspaceRole{}, autoAggregateIndexKey, workspaceRoleIndexByAnnotation); err != nil {
		return err
	}
	if err := mgr.GetCache().IndexField(context.Background(), &iamv1beta1.ClusterRole{}, autoAggregateIndexKey, clusterRoleIndexByAnnotation); err != nil {
		return err
	}
	if err := mgr.GetCache().IndexField(context.Background(), &iamv1beta1.Role{}, autoAggregateIndexKey, roleIndexByAnnotation); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&iamv1beta1.RoleTemplate{}).
		Complete(r)
}

func globalRoleIndexByAnnotation(obj client.Object) []string {
	role := obj.(*iamv1beta1.GlobalRole)
	if val, ok := role.Annotations[autoAggregationLabel]; ok {
		return []string{val}
	}
	return []string{}
}

func workspaceRoleIndexByAnnotation(obj client.Object) []string {
	role := obj.(*iamv1beta1.WorkspaceRole)
	if val, ok := role.Annotations[autoAggregationLabel]; ok {
		return []string{val}
	}
	return []string{}
}

func clusterRoleIndexByAnnotation(obj client.Object) []string {
	role := obj.(*iamv1beta1.ClusterRole)
	if val, ok := role.Annotations[autoAggregationLabel]; ok {
		return []string{val}
	}
	return []string{}
}

func roleIndexByAnnotation(obj client.Object) []string {
	role := obj.(*iamv1beta1.Role)
	if val, ok := role.Annotations[autoAggregationLabel]; ok {
		return []string{val}
	}
	return []string{}
}

func isContainsLabels(haystack, needle map[string]string) bool {
	var count int
	for key, val := range needle {
		if haystack[key] == val {
			count += 1
		}
	}
	return count == len(needle)
}
