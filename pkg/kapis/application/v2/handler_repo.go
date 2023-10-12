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

package v2

import (
	"fmt"
	"net/url"

	"github.com/emicklei/go-restful/v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/controller/application/installer"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/utils/idutils"
	"kubesphere.io/kubesphere/pkg/utils/stringutils"
)

func (h *appHandler) CreateOrUpdateRepo(req *restful.Request, resp *restful.Response) {

	repoRequest := &appv2.HelmRepo{}
	err := req.ReadEntity(repoRequest)
	if err != nil {
		klog.V(4).Infoln(err)
		api.HandleBadRequest(resp, nil, err)
		return
	}
	workspace := req.PathParameter("workspace")

	repoId := req.PathParameter("repo")
	if repoId == "" {
		prefix := fmt.Sprintf("%s-", repoRequest.Name)
		repoId = idutils.GetUuid36(prefix)
	}

	repo := &appv2.HelmRepo{}
	repo.Name = repoId
	parsedUrl, err := url.Parse(repoRequest.Spec.Url)
	if err != nil {
		api.HandleBadRequest(resp, nil, err)
		return
	}

	if parsedUrl.User != nil {
		repoRequest.Spec.Credential.Username = parsedUrl.User.Username()
		repoRequest.Spec.Credential.Password, _ = parsedUrl.User.Password()
	}

	_, err = installer.LoadRepoIndex(repoRequest.Spec.Url, repoRequest.Spec.Credential)
	if err != nil {
		klog.Errorf("validate repo failed, err: %s", err)
		api.HandleBadRequest(resp, nil, err)
		return
	}

	if req.QueryParameter("validate") != "" {
		data := map[string]any{"ok": true}
		resp.WriteAsJson(data)
		return
	}
	mutateFn := func() error {
		repo.Spec = appv2.HelmRepoSpec{
			Name:        repo.Name,
			Url:         parsedUrl.String(),
			SyncPeriod:  repoRequest.Spec.SyncPeriod,
			Description: stringutils.ShortenString(repoRequest.Spec.Description, 512),
		}
		if parsedUrl.User != nil {
			repo.Spec.Credential.Username = parsedUrl.User.Username()
			repo.Spec.Credential.Password, _ = parsedUrl.User.Password()
		}
		if workspace != "" {
			repo.SetLabels(map[string]string{constants.WorkspaceLabelKey: workspace})
		}

		return nil
	}

	_, err = controllerutil.CreateOrUpdate(req.Request.Context(), h.client, repo, mutateFn)
	if err != nil {
		klog.Errorln(err)
		handleError(resp, err)
		return
	}
	data := map[string]interface{}{"repo_id": repoId}

	resp.WriteAsJson(data)
}

func (h *appHandler) DeleteRepo(req *restful.Request, resp *restful.Response) {
	repoId := req.PathParameter("repo")

	err := h.client.Delete(req.Request.Context(), &appv2.HelmRepo{ObjectMeta: metav1.ObjectMeta{Name: repoId}})
	if err != nil {
		klog.Errorln(err)
		handleError(resp, err)
		return
	}
	klog.V(4).Info("delete repo: ", repoId)

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeRepo(req *restful.Request, resp *restful.Response) {
	repoId := req.PathParameter("repo")

	key := runtimeclient.ObjectKey{Name: repoId}
	repo := &appv2.HelmRepo{}
	err := h.client.Get(req.Request.Context(), key, repo)
	if requestDone(err, resp) {
		return
	}
	repo.SetManagedFields(nil)

	resp.WriteEntity(repo)
}

func (h *appHandler) ListRepos(req *restful.Request, resp *restful.Response) {

	helmRepoList := &appv2.HelmRepoList{}
	err := h.client.List(req.Request.Context(), helmRepoList)
	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(helmRepoList, req))
}

func (h *appHandler) ListRepoEvents(req *restful.Request, resp *restful.Response) {
	repoId := req.PathParameter("repo")

	list := v1.EventList{}
	selector := fields.SelectorFromSet(fields.Set{
		"involvedObject.name": repoId,
	})

	opt := &runtimeclient.ListOptions{FieldSelector: selector}
	err := h.client.List(req.Request.Context(), &list, opt)
	if requestDone(err, resp) {
		return
	}

	resp.WriteEntity(convertToListResult(&list, req))
}
