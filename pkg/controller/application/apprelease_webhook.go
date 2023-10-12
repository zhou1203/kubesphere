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

package application

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appv2 "kubesphere.io/api/application/v2"
)

func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return builder.WebhookManagedBy(mgr).
		For(&appv2.ApplicationRelease{}).
		WithDefaulter(&applicationAnnotator{}).
		Complete()
}

type applicationAnnotator struct{}

func (a *applicationAnnotator) Default(ctx context.Context, obj runtime.Object) error {
	rls, ok := obj.(*appv2.ApplicationRelease)
	if !ok {
		return fmt.Errorf("expected a ApplicationRelease but got a %T", obj)
	}
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return err
	}

	if rls.Annotations == nil {
		rls.Annotations = map[string]string{}
	}
	rls.Annotations[appv2.ReqUserAnnotationKey] = req.UserInfo.Username
	return nil
}
