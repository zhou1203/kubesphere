/*
Copyright 2020 The KubeSphere Authors.

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

package v1alpha2

const (
	MsgLen               = 512
	HelmRepoSyncStateLen = 10
	MaxStateNum          = 10

	// app version state
	StateDraft     = "draft"
	StateSubmitted = "submitted"
	StatePassed    = "passed"
	StateRejected  = "rejected"
	StateSuspended = "suspended"
	StateActive    = "active"

	// repo state
	RepoStateSuccessful = "successful"
	RepoStateFailed     = "failed"

	// app release state
	AppReleaseStatusActive      = "active"
	AppReleaseStatusCreating    = "creating"
	AppReleaseStatusDeleting    = "deleting"
	AppReleaseStatusUpgrading   = "upgrading"
	AppReleaseStatusRollbacking = "rollbacking"
	AppReleaseStatusFailed      = "failed"
	AppReleaseStatusCreated     = "created"
	AppReleaseStatusUpgraded    = "upgraded"

	AttachmentTypeScreenshot = "screenshot"
	AttachmentTypeIcon       = "icon"

	AppStoreSuffix      = "-store"
	AppIDPrefix         = "app-"
	HelmRepoIDPrefix    = "repo-"
	BuiltinRepoPrefix   = "builtin-"
	AppVersionIDPrefix  = "appv-"
	CategoryIDPrefix    = "ctg-"
	AppAttachmentPrefix = "att-"
	AppReleasePrefix    = "rls-"
	UncategorizedName   = "uncategorized"
	UncategorizedID     = "ctg-uncategorized"
	AppStoreRepoID      = "repo-helm"

	AppInstance    = "app.kubesphere.io/instance"
	AppReleaseName = "app.kubesphere.io/app-release-name"

	RepoSyncPeriod          = "app.kubesphere.io/sync-period"
	OriginWorkspaceLabelKey = "kubesphere.io/workspace-origin"

	RepoIDLabelKey       = "app.kubesphere.io/repo-id"
	AppIDLabelKey        = "app.kubesphere.io/app-id"
	AppVersionIDLabelKey = "app.kubesphere.io/app-version-id"
	CategoryIDLabelKey   = "app.kubesphere.io/app-category-id"

	AppReleaseReferenceLabelKey = "app.kubesphere.io/app-release-ref"

	ReqUserAnnotationKey = "app.kubesphere.io/req-user"
)
