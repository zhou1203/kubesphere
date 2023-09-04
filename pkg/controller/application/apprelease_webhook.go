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
	"encoding/json"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appv1alpha2 "kubesphere.io/api/application/v1alpha2"
)

type InjectorHandler struct {
	decoder *admission.Decoder
}

var _ admission.DecoderInjector = &InjectorHandler{}

// InjectDecoder injects the decoder into a InjectorHandler.
func (h *InjectorHandler) InjectDecoder(d *admission.Decoder) error {
	h.decoder = d
	return nil
}

// Handle handles admission requests.
func (h *InjectorHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	rls := &appv1alpha2.ApplicationRelease{}
	if err := h.decoder.Decode(req, rls); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if rls.Annotations == nil {
		rls.Annotations = map[string]string{}
	}
	rls.Annotations[appv1alpha2.ReqUserAnnotationKey] = req.UserInfo.Username

	marshaledRls, err := json.Marshal(rls)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledRls)
}
