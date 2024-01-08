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

package user

import (
	"context"
	"fmt"
	"net/mail"

	kscontroller "kubesphere.io/kubesphere/pkg/controller"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
)

const webhookName = "user-webhook"

func (v *Webhook) Name() string {
	return webhookName
}

var _ kscontroller.Controller = &Webhook{}
var _ admission.CustomValidator = &Webhook{}

type Webhook struct {
	client.Client
}

func (v *Webhook) SetupWithManager(mgr *kscontroller.Manager) error {
	v.Client = mgr.GetClient()
	return builder.WebhookManagedBy(mgr).
		For(&iamv1beta1.User{}).
		WithValidator(v).
		Complete()
}

// validate admits a pod if a specific annotation exists.
func (v *Webhook) validate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	user, ok := obj.(*iamv1beta1.User)
	if !ok {
		return nil, fmt.Errorf("expected a User but got a %T", obj)
	}

	allUsers := iamv1beta1.UserList{}
	if err := v.List(ctx, &allUsers, &client.ListOptions{}); err != nil {
		return nil, err
	}

	if _, err := mail.ParseAddress(user.Spec.Email); user.Spec.Email != "" && err != nil {
		return nil, fmt.Errorf("invalid email address:%s", user.Spec.Email)
	}

	alreadyExist := emailAlreadyExist(allUsers, user)
	if alreadyExist {
		return nil, fmt.Errorf("user email: %s already exists", user.Spec.Email)
	}
	return nil, nil
}

func (v *Webhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, obj)
}

func (v *Webhook) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, newObj)
}

func (v *Webhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, obj)
}

func emailAlreadyExist(users iamv1beta1.UserList, user *iamv1beta1.User) bool {
	// empty email is allowed
	if user.Spec.Email == "" {
		return false
	}
	for _, exist := range users.Items {
		if exist.Spec.Email == user.Spec.Email && exist.Name != user.Name {
			return true
		}
	}
	return false
}
