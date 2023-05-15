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
	KubeSystemNamespace        = "kube-system"
	IstioNamespace             = "istio-system"
	KubeSphereLoggingNamespace = "kubesphere-logging-system"
	KubeSphereNamespace        = "kubesphere-system"
	KubeSphereControlNamespace = "kubesphere-controls-system"
	KubeSphereAPIServerName    = "ks-apiserver"
	PorterNamespace            = "porter-system"
	KubeSphereConfigName       = "kubesphere-config"
	KubeSphereConfigMapDataKey = "kubesphere.yaml"
	KubectlPodNamePrefix       = "ks-managed-kubectl"

	WorkspaceLabelKey        = "kubesphere.io/workspace"
	NamespaceLabelKey        = "kubesphere.io/namespace"
	DisplayNameAnnotationKey = "kubesphere.io/alias-name"
	CreatorAnnotationKey     = "kubesphere.io/creator"
	UsernameLabelKey         = "kubesphere.io/username"
	KubefedManagedLabel      = "kubefed.io/managed"

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

	NetworkTopologyTag = "Network Topology"

	LogQueryTag      = "Log Query"
	EventsQueryTag   = "Events Query"
	AuditingQueryTag = "Auditing Query"

	// SuccessSynced is used as part of the Event 'reason' when a resource is synced
	SuccessSynced = "Synced"
	// FailedSynced is used as part of the Event 'reason' when a resource is not synced
	FailedSynced          = "FailedSync"
	MessageResourceSynced = "Synced successfully"
)

var (
	SystemNamespaces = []string{KubeSphereNamespace, KubeSphereLoggingNamespace, KubeSystemNamespace, IstioNamespace, PorterNamespace}
)
