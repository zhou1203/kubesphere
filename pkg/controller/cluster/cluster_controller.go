/*
Copyright 2020 KubeSphere Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"kubesphere.io/kubesphere/pkg/constants"
	kscontroller "kubesphere.io/kubesphere/pkg/controller"
	clusterutils "kubesphere.io/kubesphere/pkg/controller/cluster/utils"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
	"kubesphere.io/kubesphere/pkg/utils/k8sutil"
	"kubesphere.io/kubesphere/pkg/version"
)

// Cluster controller only runs under multicluster mode. Cluster controller is following below steps,
//   1. Wait for cluster agent is ready if connection type is proxy
//   2. Join cluster into federation control plane if kubeconfig is ready.
//   3. Pull cluster version, set result to cluster status
// Also put all clusters back into queue every 5 * time.Minute to sync cluster status, this is needed
// in case there aren't any cluster changes made.
// Also check if all the clusters are ready by the spec.connection.kubeconfig every resync period

const (
	controllerName    = "cluster"
	kubesphereManaged = "kubesphere.io/managed"
)

// Cluster template for reconcile host cluster if there is none.
var hostCluster = &clusterv1alpha1.Cluster{
	ObjectMeta: metav1.ObjectMeta{
		Name: "host",
		Annotations: map[string]string{
			"kubesphere.io/description": "The description was created by KubeSphere automatically. " +
				"It is recommended that you use the Host Cluster to manage clusters only " +
				"and deploy workloads on Member Clusters.",
		},
		Labels: map[string]string{
			clusterv1alpha1.HostCluster: "",
			kubesphereManaged:           "true",
		},
	},
	Spec: clusterv1alpha1.ClusterSpec{
		Provider: "kubesphere",
		Connection: clusterv1alpha1.Connection{
			Type: clusterv1alpha1.ConnectionTypeDirect,
		},
	},
}

var _ kscontroller.Controller = &Reconciler{}
var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Name() string {
	return controllerName
}

func (r *Reconciler) Enabled(clusterRole string) bool {
	return strings.EqualFold(clusterRole, string(clusterv1alpha1.ClusterRoleHost))
}

type Reconciler struct {
	client.Client
	hostConfig      *rest.Config
	hostClusterName string
	resyncPeriod    time.Duration
	installLock     sync.Map
	clusterClient   clusterclient.Interface
	clusterUID      types.UID
}

// SetupWithManager setups the Reconciler with manager.
func (r *Reconciler) SetupWithManager(mgr *kscontroller.Manager) error {
	kubeSystem, err := mgr.K8sClient.CoreV1().Namespaces().Get(context.Background(), metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		return err
	}
	r.hostConfig = mgr.K8sClient.Config()
	r.clusterClient = mgr.ClusterClient
	r.hostClusterName = mgr.MultiClusterOptions.HostClusterName
	r.resyncPeriod = mgr.MultiClusterOptions.ClusterControllerResyncPeriod
	r.clusterUID = kubeSystem.UID
	r.Client = mgr.GetClient()
	if err := mgr.Add(r); err != nil {
		return fmt.Errorf("unable to add cluster-controller to manager: %v", err)
	}
	return builder.
		ControllerManagedBy(mgr).
		For(
			&clusterv1alpha1.Cluster{},
			builder.WithPredicates(
				clusterChangedPredicate{},
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 3,
		}).
		Complete(r)
}

type clusterChangedPredicate struct {
	predicate.Funcs
}

func (clusterChangedPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}

	oldCluster := e.ObjectOld.(*clusterv1alpha1.Cluster)
	newCluster := e.ObjectNew.(*clusterv1alpha1.Cluster)
	if !reflect.DeepEqual(oldCluster.Spec, newCluster.Spec) || newCluster.DeletionTimestamp != nil {
		return true
	}
	return false
}

// NeedLeaderElection implements the LeaderElectionRunnable interface,
// controllers need to be run in leader election mode.
func (r *Reconciler) NeedLeaderElection() bool {
	return true
}

func (r *Reconciler) Start(ctx context.Context) error {
	// refresh cluster configz every resync period
	go wait.Until(func() {
		if err := r.resyncClusters(); err != nil {
			klog.Errorf("failed to reconcile cluster ready status, err: %v", err)
		}
	}, r.resyncPeriod, ctx.Done())
	return nil
}

func (r *Reconciler) resyncClusters() error {
	clusters := &clusterv1alpha1.ClusterList{}
	if err := r.List(context.TODO(), clusters); err != nil {
		return err
	}

	// no host cluster, create one
	if len(clusters.Items) == 0 {
		hostKubeConfig, err := buildKubeConfigFromRestConfig(r.hostConfig)
		if err != nil {
			return err
		}

		hostCluster.Spec.Connection.KubeConfig = hostKubeConfig
		hostCluster.Name = r.hostClusterName
		if err = r.Create(context.TODO(), hostCluster); err != nil {
			return err
		}
	}

	for _, cluster := range clusters.Items {
		if _, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: cluster.Name}}); err != nil {
			klog.Errorf("resync cluster %s failed: %v", cluster.Name, err)
		}
	}
	return nil
}

// Reconcile reconciles the Cluster object.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.Infof("Starting to sync cluster %s", req.Name)
	startTime := time.Now()

	defer func() {
		klog.Infof("Finished syncing cluster %s in %s", req.Name, time.Since(startTime))
	}()

	cluster := &clusterv1alpha1.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The object is being deleted
	if !cluster.ObjectMeta.DeletionTimestamp.IsZero() {
		if !sets.New(cluster.ObjectMeta.Finalizers...).Has(clusterv1alpha1.Finalizer) {
			return ctrl.Result{}, nil
		}

		// cleanup after cluster has been deleted
		if err := r.cleanup(ctx, cluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("cleanup for cluster %s failed: %s", cluster.Name, err.Error())
		}
		if err := r.syncClusterMembers(ctx, cluster); err != nil {
			klog.Errorf("Failed to sync cluster members for %s: %v", req.Name, err)
			return ctrl.Result{}, err
		}

		// remove our cluster finalizer
		finalizers := sets.New(cluster.ObjectMeta.Finalizers...)
		finalizers.Delete(clusterv1alpha1.Finalizer)
		cluster.ObjectMeta.Finalizers = finalizers.UnsortedList()
		return ctrl.Result{}, r.Update(ctx, cluster)
	}

	// The object is not being deleted, so if it does not have our finalizer,
	// then lets add the finalizer and update the object. This is equivalent
	// registering our finalizer.
	if !sets.New(cluster.ObjectMeta.Finalizers...).Has(clusterv1alpha1.Finalizer) {
		cluster.ObjectMeta.Finalizers = append(cluster.ObjectMeta.Finalizers, clusterv1alpha1.Finalizer)
		return ctrl.Result{}, r.Update(ctx, cluster)
	}

	if len(cluster.Spec.Connection.KubeConfig) == 0 {
		klog.V(5).Infof("Skipping to join cluster %s cause the kubeconfig is empty", cluster.Name)
		return ctrl.Result{}, nil
	}

	clusterClient, err := r.clusterClient.GetClusterClient(cluster.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster client for %s: %s", cluster.Name, err)
	}
	// cluster is ready, we can pull kubernetes cluster info through agent
	// since there is no agent necessary for host cluster, so updates for host cluster
	// is safe.
	if len(cluster.Spec.Connection.KubernetesAPIEndpoint) == 0 {
		cluster.Spec.Connection.KubernetesAPIEndpoint = clusterClient.RestConfig.Host
		return ctrl.Result{}, r.Update(ctx, cluster)
	}

	cluster.Status.KubernetesVersion = clusterClient.KubernetesVersion

	nodes := &corev1.NodeList{}
	if err = clusterClient.Client.List(ctx, nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster nodes: %s", err.Error())
	}
	cluster.Status.NodeCount = len(nodes.Items)

	// Use kube-system namespace UID as cluster ID
	kubeSystem := &corev1.Namespace{}
	if err = clusterClient.Client.Get(ctx, client.ObjectKey{Name: metav1.NamespaceSystem}, kubeSystem); err != nil {
		return ctrl.Result{}, err
	}
	cluster.Status.UID = kubeSystem.UID

	if r.checkIfClusterIsHostCluster(kubeSystem.UID) {
		return r.reconcileHostCluster(ctx, cluster, clusterClient.KubernetesClient)
	}
	return r.reconcileMemberCluster(ctx, cluster, clusterClient.KubernetesClient)
}

func (r *Reconciler) reconcileHostCluster(ctx context.Context, cluster *clusterv1alpha1.Cluster, clusterClient kubernetes.Interface) (ctrl.Result, error) {
	hostKubeConfig, err := buildKubeConfigFromRestConfig(r.hostConfig)
	if err != nil {
		return ctrl.Result{}, err
	}
	// update host cluster config
	if !bytes.Equal(cluster.Spec.Connection.KubeConfig, hostKubeConfig) {
		cluster.Spec.Connection.KubeConfig = hostKubeConfig
	}
	if cluster.Labels == nil {
		cluster.Labels = make(map[string]string)
	}
	cluster.Labels[clusterv1alpha1.HostCluster] = ""
	return r.syncClusterStatus(ctx, cluster, clusterClient)
}

func (r *Reconciler) reconcileMemberCluster(ctx context.Context, cluster *clusterv1alpha1.Cluster, clusterClient kubernetes.Interface) (ctrl.Result, error) {
	// Install KS Core in member cluster
	if !hasCondition(cluster.Status.Conditions, clusterv1alpha1.ClusterKSCoreReady) {
		// get the lock, make sure only one thread is executing the helm task
		if _, ok := r.installLock.Load(cluster.Name); ok {
			return ctrl.Result{}, nil
		}
		r.installLock.Store(cluster.Name, "")
		defer r.installLock.Delete(cluster.Name)
		klog.Infof("Starting installing KS Core for the cluster %s", cluster.Name)
		defer klog.Infof("Finished installing KS Core for the cluster %s", cluster.Name)
		hostConfig, err := getKubeSphereConfig(ctx, r.Client)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err = installKSCoreInMemberCluster(string(cluster.Spec.Connection.KubeConfig), hostConfig.AuthenticationOptions.JwtSecret); err != nil {
			return ctrl.Result{}, err
		}
		r.updateClusterCondition(cluster, clusterv1alpha1.ClusterCondition{
			Type:               clusterv1alpha1.ClusterKSCoreReady,
			Status:             corev1.ConditionTrue,
			LastUpdateTime:     metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             clusterv1alpha1.ClusterKSCoreReady,
			Message:            "KS Core is available now",
		})
		if err = r.Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 3}, nil
	}
	if err := r.updateKubeConfigExpirationDateCondition(cluster); err != nil {
		// should not block the whole process
		klog.Warningf("sync KubeConfig expiration date for cluster %s failed: %v", cluster.Name, err)
	}
	return r.syncClusterStatus(ctx, cluster, clusterClient)
}

func (r *Reconciler) syncClusterStatus(ctx context.Context, cluster *clusterv1alpha1.Cluster, clusterClient kubernetes.Interface) (ctrl.Result, error) {
	v, err := r.tryFetchKubeSphereVersion(ctx, clusterClient)
	if err != nil {
		// The KubeSphere service is unavailable
		klog.Errorf("failed to get KubeSphere version, err: %#v", err)
		r.updateClusterCondition(cluster, clusterv1alpha1.ClusterCondition{
			Type:               clusterv1alpha1.ClusterReady,
			Status:             corev1.ConditionFalse,
			LastUpdateTime:     metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             string(clusterv1alpha1.ClusterReady),
			Message:            err.Error(),
		})
		return ctrl.Result{}, r.Update(ctx, cluster)
	}

	cluster.Status.KubeSphereVersion = v
	readyCondition := clusterv1alpha1.ClusterCondition{
		Type:               clusterv1alpha1.ClusterReady,
		Status:             corev1.ConditionTrue,
		LastUpdateTime:     metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             string(clusterv1alpha1.ClusterReady),
		Message:            "Cluster is available now",
	}
	r.updateClusterCondition(cluster, readyCondition)
	if err = r.Update(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}
	if err = r.setClusterNameInNamespace(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}
	if err = r.syncClusterMembers(ctx, cluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to sync cluster membership for %s: %s", cluster.Name, err)
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) setClusterNameInNamespace(ctx context.Context, cluster *clusterv1alpha1.Cluster) error {
	clusterClient, err := r.clusterClient.GetRuntimeClient(cluster.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %s", err)
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kubeSphereNamespace := &corev1.Namespace{}
		if err = clusterClient.Get(ctx, client.ObjectKey{Name: constants.KubeSphereNamespace}, kubeSphereNamespace); err != nil {
			return err
		}
		annotations := kubeSphereNamespace.Annotations
		if annotations[clusterv1alpha1.AnnotationClusterName] == cluster.Name &&
			annotations[clusterv1alpha1.AnnotationHostClusterName] == r.hostClusterName {
			return nil
		}
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[clusterv1alpha1.AnnotationClusterName] = cluster.Name
		annotations[clusterv1alpha1.AnnotationHostClusterName] = r.hostClusterName
		kubeSphereNamespace.Annotations = annotations
		return clusterClient.Update(ctx, kubeSphereNamespace)
	})
}

func (r *Reconciler) checkIfClusterIsHostCluster(clusterKubeSystemUID types.UID) bool {
	return r.clusterUID == clusterKubeSystemUID
}

func (r *Reconciler) tryFetchKubeSphereVersion(ctx context.Context, clusterClient kubernetes.Interface) (string, error) {
	response, err := clusterClient.CoreV1().Services(constants.KubeSphereNamespace).
		ProxyGet("http", constants.KubeSphereAPIServerName, "80", "/version", nil).
		DoRaw(ctx)
	if err != nil {
		return "", err
	}

	info := version.Info{}
	if err = json.Unmarshal(response, &info); err != nil {
		return "", err
	}

	// currently, we kubesphere v2.1 can not be joined as a member cluster and it will never be reconciled,
	// so we don't consider that situation
	// for kubesphere v3.0.0, the gitVersion is always v0.0.0, so we return v3.0.0
	if info.GitVersion == "v0.0.0" {
		return "v3.0.0", nil
	}

	if len(info.GitVersion) == 0 {
		return "unknown", nil
	}

	return info.GitVersion, nil
}

// updateClusterCondition updates condition in cluster conditions using giving condition
// adds condition if not existed
func (r *Reconciler) updateClusterCondition(cluster *clusterv1alpha1.Cluster, condition clusterv1alpha1.ClusterCondition) {
	if cluster.Status.Conditions == nil {
		cluster.Status.Conditions = make([]clusterv1alpha1.ClusterCondition, 0)
	}

	newConditions := make([]clusterv1alpha1.ClusterCondition, 0)
	for _, cond := range cluster.Status.Conditions {
		if cond.Type == condition.Type {
			continue
		}
		newConditions = append(newConditions, cond)
	}

	newConditions = append(newConditions, condition)
	cluster.Status.Conditions = newConditions
}

func parseKubeConfigExpirationDate(kubeconfig []byte) (time.Time, error) {
	config, err := k8sutil.LoadKubeConfigFromBytes(kubeconfig)
	if err != nil {
		return time.Time{}, err
	}
	if config.CertData == nil {
		// an empty CertData will be treated as never expiring,
		// such as some kubeconfig files that use token authentication do not have this field
		return time.Time{}, nil
	}
	block, _ := pem.Decode(config.CertData)
	if block == nil {
		return time.Time{}, fmt.Errorf("pem.Decode failed, got empty block data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

func (r *Reconciler) updateKubeConfigExpirationDateCondition(cluster *clusterv1alpha1.Cluster) error {
	// we don't need to check member clusters which using proxy mode, their certs are managed and will be renewed by tower.
	if cluster.Spec.Connection.Type == clusterv1alpha1.ConnectionTypeProxy {
		return nil
	}

	klog.V(5).Infof("sync KubeConfig expiration date for cluster %s", cluster.Name)
	notAfter, err := parseKubeConfigExpirationDate(cluster.Spec.Connection.KubeConfig)
	if err != nil {
		return fmt.Errorf("parseKubeConfigExpirationDate for cluster %s failed: %v", cluster.Name, err)
	}

	expiresInSevenDays := corev1.ConditionFalse
	expirationDate := ""
	// empty expiration date will be treated as never expiring
	if !notAfter.IsZero() {
		expirationDate = notAfter.String()
		if time.Now().AddDate(0, 0, 7).Sub(notAfter) > 0 {
			expiresInSevenDays = corev1.ConditionTrue
		}
	}

	r.updateClusterCondition(cluster, clusterv1alpha1.ClusterCondition{
		Type:               clusterv1alpha1.ClusterKubeConfigCertExpiresInSevenDays,
		Status:             expiresInSevenDays,
		LastUpdateTime:     metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             string(clusterv1alpha1.ClusterKubeConfigCertExpiresInSevenDays),
		Message:            expirationDate,
	})
	return nil
}

// syncClusterMembers Sync granted clusters for users periodically
func (r *Reconciler) syncClusterMembers(ctx context.Context, cluster *clusterv1alpha1.Cluster) error {
	users := &iamv1beta1.UserList{}
	if err := r.List(ctx, users); err != nil {
		return err
	}

	grantedUsers := sets.New[string]()
	clusterName := cluster.Name
	if cluster.DeletionTimestamp.IsZero() {
		clusterClient, err := r.clusterClient.GetRuntimeClient(cluster.Name)
		if err != nil {
			return fmt.Errorf("failed to get cluster client: %s", err)
		}

		clusterRoleBindings := &iamv1beta1.ClusterRoleBindingList{}
		if err := clusterClient.List(ctx, clusterRoleBindings, client.HasLabels{iamv1beta1.UserReferenceLabel}); err != nil {
			return fmt.Errorf("failed to list clusterrolebindings: %s", err)
		}
		for _, clusterRoleBinding := range clusterRoleBindings.Items {
			for _, sub := range clusterRoleBinding.Subjects {
				if sub.Kind == iamv1beta1.ResourceKindUser {
					grantedUsers.Insert(sub.Name)
				}
			}
		}
	}

	for i := range users.Items {
		user := &users.Items[i]
		grantedClustersAnnotation := user.Annotations[iamv1beta1.GrantedClustersAnnotation]
		var grantedClusters sets.Set[string]
		if len(grantedClustersAnnotation) > 0 {
			grantedClusters = sets.New(strings.Split(grantedClustersAnnotation, ",")...)
		} else {
			grantedClusters = sets.New[string]()
		}
		if grantedUsers.Has(user.Name) && !grantedClusters.Has(clusterName) {
			grantedClusters.Insert(clusterName)
		} else if !grantedUsers.Has(user.Name) && grantedClusters.Has(clusterName) {
			grantedClusters.Delete(clusterName)
		}
		grantedClustersAnnotation = strings.Join(grantedClusters.UnsortedList(), ",")
		if user.Annotations[iamv1beta1.GrantedClustersAnnotation] != grantedClustersAnnotation {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := r.Get(ctx, types.NamespacedName{Name: user.Name}, user); err != nil {
					return err
				}
				if user.Annotations == nil {
					user.Annotations = make(map[string]string, 0)
				}
				user.Annotations[iamv1beta1.GrantedClustersAnnotation] = grantedClustersAnnotation
				return r.Update(ctx, user)
			})
			if err != nil {
				return fmt.Errorf("failed to update granted clusters annotation: %s", err)
			}
		}
	}
	return nil
}

func (r *Reconciler) cleanup(ctx context.Context, cluster *clusterv1alpha1.Cluster) error {
	if !clusterutils.IsClusterReady(cluster) {
		return nil
	}

	clusterClient, err := r.clusterClient.GetRuntimeClient(cluster.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %s", err)
	}
	kubeSphereNamespace := &corev1.Namespace{}
	if err = clusterClient.Get(ctx, client.ObjectKey{Name: constants.KubeSphereNamespace}, kubeSphereNamespace); err != nil {
		return client.IgnoreNotFound(err)
	}
	delete(kubeSphereNamespace.Annotations, clusterv1alpha1.AnnotationClusterName)
	delete(kubeSphereNamespace.Annotations, clusterv1alpha1.AnnotationHostClusterName)
	return clusterClient.Update(ctx, kubeSphereNamespace)
}
