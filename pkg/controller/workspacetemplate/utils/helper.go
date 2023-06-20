package utils

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	"kubesphere.io/api/cluster/v1alpha1"
	tenantv1alpha2 "kubesphere.io/api/tenant/v1alpha2"
)

func WorkspaceTemplateMatchTargetCluster(workspaceTemplate *tenantv1alpha2.WorkspaceTemplate, cluster *v1alpha1.Cluster) bool {
	match := false
	if len(workspaceTemplate.Spec.Placement.Clusters) > 0 {
		for _, clusterRef := range workspaceTemplate.Spec.Placement.Clusters {
			if clusterRef.Name == cluster.Name {
				match = true
				break
			}
		}
	} else if workspaceTemplate.Spec.Placement.ClusterSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(workspaceTemplate.Spec.Placement.ClusterSelector)
		if err != nil {
			klog.Errorf("failed to parse cluster selector: %s", err)
			return false
		}
		match = selector.Matches(labels.Set(cluster.Labels))
	}
	return match
}
