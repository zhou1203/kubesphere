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

	"kubesphere.io/kubesphere/pkg/api"

	"github.com/emicklei/go-restful/v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/controller/application/installer"
	"kubesphere.io/kubesphere/pkg/server/errors"
	"kubesphere.io/kubesphere/pkg/utils/stringutils"
)

func (h *appHandler) CreateOrUpdateRepo(req *restful.Request, resp *restful.Response) {

	repoRequest := &appv2.Repo{}
	err := req.ReadEntity(repoRequest)
	if requestDone(err, resp) {
		return
	}

	repoId := req.PathParameter("repo")
	if repoId == "" {
		repoId = repoRequest.Name
	}
	repo := &appv2.Repo{}
	repo.Name = repoId

	if h.CheckExisted(req, runtimeclient.ObjectKey{Name: repo.Name}, repo) {
		api.HandleConflict(resp, req, fmt.Errorf("repo %s already exists", repo.Name))
		return
	}

	parsedUrl, err := url.Parse(repoRequest.Spec.Url)
	if requestDone(err, resp) {
		return
	}

	if parsedUrl.User != nil {
		repoRequest.Spec.Credential.Username = parsedUrl.User.Username()
		repoRequest.Spec.Credential.Password, _ = parsedUrl.User.Password()
	}

	_, err = installer.LoadRepoIndex(repoRequest.Spec.Url, repoRequest.Spec.Credential)
	if requestDone(err, resp) {
		return
	}

	if req.QueryParameter("validate") != "" {
		data := map[string]any{"ok": true}
		resp.WriteAsJson(data)
		return
	}

	workspace := req.QueryParameter("workspace")
	if workspace == "" {
		workspace = appv2.SystemWorkspace
	}
	mutateFn := func() error {
		repo.Spec = appv2.RepoSpec{
			Url:         parsedUrl.String(),
			SyncPeriod:  repoRequest.Spec.SyncPeriod,
			Description: stringutils.ShortenString(repoRequest.Spec.Description, 512),
		}
		if parsedUrl.User != nil {
			repo.Spec.Credential.Username = parsedUrl.User.Username()
			repo.Spec.Credential.Password, _ = parsedUrl.User.Password()
		}
		if repo.GetLabels() == nil {
			repo.SetLabels(map[string]string{})
		}
		repo.Labels[constants.WorkspaceLabelKey] = workspace

		if repo.GetAnnotations() == nil {
			repo.SetAnnotations(map[string]string{})
		}
		if repoRequest.GetAnnotations()[constants.DisplayNameAnnotationKey] != "" {
			repo.Annotations[constants.DisplayNameAnnotationKey] = repoRequest.GetAnnotations()[constants.DisplayNameAnnotationKey]
		}

		return nil
	}

	_, err = controllerutil.CreateOrUpdate(req.Request.Context(), h.client, repo, mutateFn)
	if requestDone(err, resp) {
		return
	}
	data := map[string]interface{}{"repo_id": repoId}

	resp.WriteAsJson(data)
}

func (h *appHandler) DeleteRepo(req *restful.Request, resp *restful.Response) {
	repoId := req.PathParameter("repo")

	err := h.client.Delete(req.Request.Context(), &appv2.Repo{ObjectMeta: metav1.ObjectMeta{Name: repoId}})
	if requestDone(err, resp) {
		return
	}
	klog.V(4).Info("delete repo: ", repoId)

	resp.WriteEntity(errors.None)
}

func (h *appHandler) DescribeRepo(req *restful.Request, resp *restful.Response) {
	repoId := req.PathParameter("repo")

	key := runtimeclient.ObjectKey{Name: repoId}
	repo := &appv2.Repo{}
	err := h.client.Get(req.Request.Context(), key, repo)
	if requestDone(err, resp) {
		return
	}
	repo.SetManagedFields(nil)

	resp.WriteEntity(repo)
}

func (h *appHandler) ListRepos(req *restful.Request, resp *restful.Response) {

	helmRepoList := &appv2.RepoList{}
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
