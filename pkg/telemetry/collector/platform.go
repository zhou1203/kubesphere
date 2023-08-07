/*
Copyright 2023 The KubeSphere Authors.

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

package collector

import (
	"context"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
)

func init() {
	register(&Project{})
}

type Project struct {
	Workspace int `json:"workspace"`
	User      int `json:"user"`
}

func (p Project) RecordKey() string {
	return "platform"
}

func (p Project) Collect(opts *CollectorOpts) interface{} {
	workspaceList := &tenantv1alpha1.WorkspaceList{}
	userList := &iamv1beta1.UserList{}

	// counting the number of workspace
	if err := opts.Client.List(opts.Ctx, workspaceList); err != nil {
		return nil
	}
	p.Workspace = len(workspaceList.Items)

	// counting the number of user
	if err := opts.Client.List(context.Background(), userList); err != nil {
		return nil
	}
	p.User = len(userList.Items)

	return p
}
