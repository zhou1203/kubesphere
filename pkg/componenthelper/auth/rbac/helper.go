package rbac

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/utils/sliceutil"
)

const (
	AggregateRoleTemplateFailed = "AggregateRoleTemplateFailed"
	MessageResourceSynced       = "Aggregating roleTemplates successfully"
)

type Helper struct {
	client.Client
}

func NewHelper(c client.Client) *Helper {
	return &Helper{c}
}

func (h *Helper) GetAggregationRoleTemplateRule(ctx context.Context, owner RuleOwner) ([]rbacv1.PolicyRule, []string, error) {
	aggregationRule := owner.GetAggregationRule()
	rules := make([]rbacv1.PolicyRule, 0)
	newTemplateNames := make([]string, 0)
	if aggregationRule == nil {
		return owner.GetRules(), newTemplateNames, nil
	}

	logger := klog.FromContext(ctx)
	if aggregationRule.RoleSelector == nil {
		for _, templateName := range aggregationRule.TemplateNames {
			roleTemplate := &iamv1beta1.RoleTemplate{}
			if err := h.Get(ctx, types.NamespacedName{Name: templateName}, roleTemplate); err != nil {
				if errors.IsNotFound(err) {
					logger.V(4).Info("aggregation role template not found", "name", templateName, "role", owner.GetObject())
					continue
				}
				return nil, nil, fmt.Errorf("failed to fetch role template: %s", err)
			}
			newTemplateNames = append(newTemplateNames, roleTemplate.Name)
			for _, rule := range roleTemplate.Spec.Rules {
				if !ruleExists(rules, rule) {
					rules = append(rules, rule)
				}
			}
		}
	} else {
		selector := aggregationRule.RoleSelector.DeepCopy()
		roleTemplateList := &iamv1beta1.RoleTemplateList{}
		// Ensure the roleTemplate can be aggregated at the specific role scope
		selector.MatchLabels = labels.Merge(selector.MatchLabels, map[string]string{iamv1beta1.ScopeLabel: owner.GetRuleOwnerScope()})
		asSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			logger.Error(err, "failed to parse role selector", "scope", owner.GetRuleOwnerScope(), "name", owner.GetName())
			return rules, newTemplateNames, nil
		}
		if err = h.List(ctx, roleTemplateList, &client.ListOptions{LabelSelector: asSelector}); err != nil {
			return nil, nil, err
		}
		for _, roleTemplate := range roleTemplateList.Items {
			newTemplateNames = append(newTemplateNames, roleTemplate.Name)
			for _, rule := range roleTemplate.Spec.Rules {
				if !ruleExists(rules, rule) {
					rules = append(rules, rule)
				}
			}
		}
	}

	return rules, newTemplateNames, nil
}

func (h *Helper) AggregationRole(ctx context.Context, ruleOwner RuleOwner, recorder record.EventRecorder) error {
	if ruleOwner.GetAggregationRule() == nil {
		return nil
	}
	newPolicyRules, newTemplateNames, err := h.GetAggregationRoleTemplateRule(ctx, ruleOwner)
	if err != nil {
		recorder.Event(ruleOwner.GetObject(), corev1.EventTypeWarning, AggregateRoleTemplateFailed, err.Error())
		return err
	}

	cover, _ := Covers(ruleOwner.GetRules(), newPolicyRules)

	aggregationRule := ruleOwner.GetAggregationRule()
	templateNamesEqual := false
	if aggregationRule != nil {
		templateNamesEqual = sliceutil.Equal(aggregationRule.TemplateNames, newTemplateNames)
	}

	if cover && templateNamesEqual {
		return nil
	}

	if !cover {
		ruleOwner.SetRules(newPolicyRules)
	}

	if !templateNamesEqual {
		aggregationRule.TemplateNames = newTemplateNames
		ruleOwner.SetAggregationRule(aggregationRule)
	}

	if err = h.Update(ctx, ruleOwner.GetObject().(client.Object)); err != nil {
		recorder.Event(ruleOwner.GetObject(), corev1.EventTypeWarning, AggregateRoleTemplateFailed, err.Error())
		return err
	}
	recorder.Event(ruleOwner.GetObject(), corev1.EventTypeNormal, "Synced", MessageResourceSynced)
	return nil
}

func ruleExists(haystack []rbacv1.PolicyRule, needle rbacv1.PolicyRule) bool {
	for _, curr := range haystack {
		if equality.Semantic.DeepEqual(curr, needle) {
			return true
		}
	}
	return false
}
