package utils

import (
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ClusterStatusChangedPredicate struct {
	predicate.Funcs
}

func (ClusterStatusChangedPredicate) Update(e event.UpdateEvent) bool {
	oldCluster := e.ObjectOld.(*clusterv1alpha1.Cluster)
	newCluster := e.ObjectNew.(*clusterv1alpha1.Cluster)
	// cluster is ready
	if !IsClusterReady(oldCluster) && IsClusterReady(newCluster) {
		return true
	}
	return false
}

func (ClusterStatusChangedPredicate) Create(_ event.CreateEvent) bool {
	return false
}

func (ClusterStatusChangedPredicate) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ClusterStatusChangedPredicate) Generic(_ event.GenericEvent) bool {
	return false
}
