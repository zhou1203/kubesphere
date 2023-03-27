/*
Copyright 2019 The KubeSphere Authors.

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

package constants

const (
	APIVersion = "v1alpha1"

	KubeSystemNamespace        = "kube-system"
	IstioNamespace             = "istio-system"
	KubeSphereLoggingNamespace = "kubesphere-logging-system"
	KubeSphereNamespace        = "kubesphere-system"
	KubeSphereControlNamespace = "kubesphere-controls-system"
	PorterNamespace            = "porter-system"
	IngressControllerNamespace = KubeSphereControlNamespace
	AdminUserName              = "admin"
	IngressControllerPrefix    = "kubesphere-router-"
	KubeSphereConfigName       = "kubesphere-config"
	KubeSphereConfigMapDataKey = "kubesphere.yaml"

	ClusterNameLabelKey      = "kubesphere.io/cluster"
	NameLabelKey             = "kubesphere.io/name"
	WorkspaceLabelKey        = "kubesphere.io/workspace"
	NamespaceLabelKey        = "kubesphere.io/namespace"
	DisplayNameAnnotationKey = "kubesphere.io/alias-name"
	DanglingAppCleanupKey    = "application.kubesphere.io/app-cleanup"
	CreatorAnnotationKey     = "kubesphere.io/creator"
	UsernameLabelKey         = "kubesphere.io/username"
	KubefedManagedLabel      = "kubefed.io/managed"

	UserNameHeader = "X-Token-Username"

	AuthenticationTag = "Authentication"
	UserTag           = "User"
	GroupTag          = "Group"

	WorkspaceMemberTag = "Workspace Member"
	NamespaceMemberTag = "Namespace Member"
	ClusterMemberTag   = "Cluster Member"

	GlobalRoleTag    = "Global Role"
	ClusterRoleTag   = "Cluster Role"
	WorkspaceRoleTag = "Workspace Role"
	NamespaceRoleTag = "Namespace Role"

	CleanupDanglingAppOngoing = "ongoing"
	CleanupDanglingAppDone    = "done"

	ToolboxTag      = "Toolbox"
	RegistryTag     = "Docker Registry"
	GitTag          = "Git"
	TerminalTag     = "Terminal"
	MultiClusterTag = "Multi-cluster"

	WorkspaceTag    = "Workspace"
	NamespaceTag    = "Namespace"
	UserResourceTag = "User's Resources"

	NamespaceResourcesTag = "Namespace Resources"
	ClusterResourcesTag   = "Cluster Resources"
	ComponentStatusTag    = "Component Status"

	GatewayTag = "Gateway"

	NetworkTopologyTag = "Network Topology"

	KubeSphereMetricsTag = "KubeSphere Metrics"
	ClusterMetricsTag    = "Cluster Metrics"
	NodeMetricsTag       = "Node Metrics"
	NamespaceMetricsTag  = "Namespace Metrics"
	PodMetricsTag        = "Pod Metrics"
	PVCMetricsTag        = "PVC Metrics"
	IngressMetricsTag    = "Ingress Metrics"
	ContainerMetricsTag  = "Container Metrics"
	WorkloadMetricsTag   = "Workload Metrics"
	WorkspaceMetricsTag  = "Workspace Metrics"
	ComponentMetricsTag  = "Component Metrics"
	CustomMetricsTag     = "Custom Metrics"

	LogQueryTag      = "Log Query"
	EventsQueryTag   = "Events Query"
	AuditingQueryTag = "Auditing Query"

	ClusterMetersTag   = "Cluster Meters"
	NodeMetersTag      = "Node Meters"
	WorkspaceMetersTag = "Workspace Meters"
	NamespaceMetersTag = "Namespace Meters"
	WorkloadMetersTag  = "Workload Meters"
	PodMetersTag       = "Pod Meters"
	ServiceMetricsTag  = "ServiceName Meters"

	ApplicationReleaseName = "meta.helm.sh/release-name"
	ApplicationReleaseNS   = "meta.helm.sh/release-namespace"

	ApplicationName    = "app.kubernetes.io/name"
	ApplicationVersion = "app.kubernetes.io/version"

	DashboardTag = "Dashboard"
)

var (
	SystemNamespaces = []string{KubeSphereNamespace, KubeSphereLoggingNamespace, KubeSystemNamespace, IstioNamespace, PorterNamespace}
)
