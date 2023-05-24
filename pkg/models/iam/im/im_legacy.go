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

package im

import (
	"context"
	"fmt"
	"time"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/auth"
)

type LegacyIdentityManagementInterface interface {
	CreateUser(user *iamv1alpha2.User) (*iamv1alpha2.User, error)
	ListUsers(query *query.Query) (*api.ListResult, error)
	DeleteUser(username string) error
	UpdateUser(user *iamv1alpha2.User) (*iamv1alpha2.User, error)
	DescribeUser(username string) (*iamv1alpha2.User, error)
	ModifyPassword(username string, password string) error
	ListLoginRecords(username string, query *query.Query) (*api.ListResult, error)
	PasswordVerify(username string, password string) error
}

func NewLegacyOperator(client runtimeclient.Client) LegacyIdentityManagementInterface {
	return &legacyIMOperator{
		client:          client,
		resourceManager: resourcev1beta1.New(client),
	}
}

type legacyIMOperator struct {
	client          runtimeclient.Client
	resourceManager resourcev1beta1.ResourceManager
}

// UpdateUser returns user information after update.
func (im *legacyIMOperator) UpdateUser(new *iamv1alpha2.User) (*iamv1alpha2.User, error) {
	old, err := im.fetch(new.Name)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	// keep encrypted password and user status
	new.Spec.EncryptedPassword = old.Spec.EncryptedPassword
	status := old.Status
	// only support enable or disable
	if new.Status.State == iamv1alpha2.UserDisabled || new.Status.State == iamv1alpha2.UserActive {
		status.State = new.Status.State
		status.LastTransitionTime = &metav1.Time{Time: time.Now()}
	}
	new.Status = status
	if err := im.client.Update(context.Background(), new); err != nil {
		klog.Error(err)
		return nil, err
	}
	new = new.DeepCopy()
	new.Spec.EncryptedPassword = ""
	return new, nil
}

func (im *legacyIMOperator) fetch(username string) (*iamv1alpha2.User, error) {
	user := &iamv1alpha2.User{}
	if err := im.client.Get(context.Background(), types.NamespacedName{Name: username}, user); err != nil {
		return nil, err
	}
	return user.DeepCopy(), nil
}

func (im *legacyIMOperator) ModifyPassword(username string, password string) error {
	user, err := im.fetch(username)
	if err != nil {
		klog.Error(err)
		return err
	}
	user.Spec.EncryptedPassword = password
	if err = im.client.Update(context.Background(), user); err != nil {
		klog.Error(err)
		return err
	}
	return nil
}

func (im *legacyIMOperator) ListUsers(query *query.Query) (*api.ListResult, error) {
	result, err := im.resourceManager.ListResources(context.Background(), iamv1alpha2.SchemeGroupVersion.WithResource(iamv1beta1.ResourcesPluralUser), "", query)
	if err != nil {
		return nil, err
	}
	items := make([]runtime.Object, 0)
	userList := result.(*iamv1alpha2.UserList)
	for _, item := range userList.Items {
		out := item.DeepCopy()
		out.Spec.EncryptedPassword = ""
		items = append(items, out)
	}
	total := int(*userList.RemainingItemCount) + len(items)
	return &api.ListResult{Items: items, TotalItems: total}, nil
}

func (im *legacyIMOperator) PasswordVerify(username string, password string) error {
	user, err := im.fetch(username)
	if err != nil {
		return err
	}
	if err = auth.PasswordVerify(user.Spec.EncryptedPassword, password); err != nil {
		return err
	}
	return nil
}

func (im *legacyIMOperator) DescribeUser(username string) (*iamv1alpha2.User, error) {
	user, err := im.fetch(username)
	if err != nil {
		return nil, err
	}
	out := user.DeepCopy()
	out.Spec.EncryptedPassword = ""
	return out, nil
}

func (im *legacyIMOperator) DeleteUser(username string) error {
	user, err := im.fetch(username)
	if err != nil {
		return err
	}
	return im.client.Delete(context.Background(), user, &runtimeclient.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)})
}

func (im *legacyIMOperator) CreateUser(user *iamv1alpha2.User) (*iamv1alpha2.User, error) {
	if err := im.client.Create(context.Background(), user); err != nil {
		return nil, err
	}
	return user, nil
}

func (im *legacyIMOperator) ListLoginRecords(username string, q *query.Query) (*api.ListResult, error) {
	q.Filters[query.FieldLabel] = query.Value(fmt.Sprintf("%s=%s", iamv1alpha2.UserReferenceLabel, username))
	result, err := im.resourceManager.ListResources(context.Background(), iamv1alpha2.SchemeGroupVersion.WithResource(iamv1beta1.ResourcesPluralLoginRecord), "", q)
	if err != nil {
		return nil, err
	}
	items := make([]runtime.Object, 0)
	userList := result.(*iamv1alpha2.LoginRecordList)
	for _, item := range userList.Items {
		items = append(items, item.DeepCopy())
	}
	total := int(*userList.RemainingItemCount) + len(items)
	return &api.ListResult{Items: items, TotalItems: total}, nil
}
