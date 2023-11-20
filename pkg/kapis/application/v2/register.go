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
	"net/http"

	"github.com/emicklei/go-restful/v3"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/utils/clusterclient"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/models"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

type appHandler struct {
	client        runtimeclient.Client
	clusterClient clusterclient.Interface
}

func NewHandler(cacheClient runtimeclient.Client, clusterClient clusterclient.Interface) rest.Handler {

	return &appHandler{
		client:        cacheClient,
		clusterClient: clusterClient,
	}
}

func (h *appHandler) AddToContainer(c *restful.Container) error {
	mimePatch := []string{restful.MIME_JSON, runtime.MimeJsonPatchJson, runtime.MimeMergePatchJson}
	webservice := runtime.NewWebService(appv2.SchemeGroupVersion)

	// repo
	webservice.Route(webservice.POST("/repos").
		To(h.CreateOrUpdateRepo).
		Doc("Create a global repository, which is used to store package of app").
		Param(webservice.QueryParameter("validate", "Validate repository")))

	webservice.Route(webservice.DELETE("/repos/{repo}").
		To(h.DeleteRepo).
		Doc("Delete the specified global repository").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.GET("/repos").
		To(h.ListRepos).
		Doc("List global repositories").
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/repos/{repo}").
		To(h.DescribeRepo).
		Doc("Describe the specified global repository").
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.POST("/repos/{repo}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateRepo).
		Doc("Patch the specified repository in the specified workspace").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.GET("/repos/{repo}/events").
		To(h.ListRepoEvents).
		Doc("Get repository events").
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.POST("/workspaces/{workspace}/repos").
		To(h.CreateOrUpdateRepo).
		Doc("Create a global repository, which is used to store package of app").
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.QueryParameter("validate", "Validate repository")))

	webservice.Route(webservice.DELETE("/workspaces/{workspace}/repos/{repo}").
		To(h.DeleteRepo).
		Doc("Delete the specified global repository").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.GET("/workspaces/{workspace}/repos").
		To(h.ListRepos).
		Doc("List global repositories").
		Param(webservice.PathParameter("workspace", "workspace")).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/workspaces/{workspace}/repos/{repo}").
		To(h.DescribeRepo).
		Doc("Describe the specified global repository").
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.POST("/workspaces/{workspace}/repos/{repo}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateRepo).
		Doc("Patch the specified repository in the specified workspace").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("repo", "repo id")))

	webservice.Route(webservice.GET("/workspaces/{workspace}/repos/{repo}/events").
		To(h.ListRepoEvents).
		Doc("Get repository events").
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("repo", "repo id")))

	// app

	webservice.Route(webservice.POST("/apps/{app}/action").
		To(h.DoAppAction).
		Doc("Perform recover or suspend operation on app").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.POST("/apps").
		To(h.CreateOrUpdateApp).
		Doc("Create a new app template"))

	webservice.Route(webservice.POST("/workspaces/{workspace}/apps").
		To(h.CreateOrUpdateApp).
		Doc("Create a new app template").
		Param(webservice.PathParameter("workspace", "workspace")))

	webservice.Route(webservice.POST("/workspaces/{workspace}/apps/{app}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateApp).
		Doc("Patch the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.POST("/apps/{app}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateApp).
		Doc("Patch the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.PATCH("/apps/{app}").
		Consumes(mimePatch...).
		To(h.PatchApp).
		Doc("Patch the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.PATCH("/workspaces/{workspace}/apps/{app}").
		Consumes(mimePatch...).
		To(h.PatchApp).
		Doc("Patch the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/apps").
		To(h.ListApps).
		Doc("List app templates.").
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/workspaces/{workspace}/apps").
		To(h.ListApps).
		Doc("List app templates in the specified workspace.").
		Param(webservice.PathParameter("workspace", "workspace name")).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/apps/{app}").
		To(h.DescribeApp).
		Doc("Describe the specified app template").
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/workspaces/{workspace}/apps/{app}").
		To(h.DescribeApp).
		Doc("Describe the specified app template").
		Param(webservice.PathParameter("app", "app template id"))).
		Param(webservice.PathParameter("workspace", "workspace name"))

	webservice.Route(webservice.DELETE("/apps/{app}").
		To(h.DeleteApp).
		Doc("Delete the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.DELETE("/workspaces/{workspace}/apps/{app}").
		To(h.DeleteApp).
		Doc("Delete the specified app template").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace name")).
		Param(webservice.PathParameter("app", "app template id")))

	// app versions

	webservice.Route(webservice.POST("/apps/{app}/versions").
		To(h.CreateOrUpdateAppVersion).
		Doc("Create a new app template version").
		Param(webservice.QueryParameter("validate", "Validate format of package(pack by op tool)")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.POST("/workspaces/{workspace}/apps/{app}/versions").
		To(h.CreateOrUpdateAppVersion).
		Doc("Create a new app template version").
		Param(webservice.QueryParameter("validate", "Validate format of package(pack by op tool)")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.DELETE("/apps/{app}/versions/{version}").
		To(h.DeleteAppVersion).
		Doc("Delete the specified app template version").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/apps/{app}/versions/{version}").
		To(h.DescribeAppVersion).
		Doc("Describe the specified app template version").
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.DELETE("/workspaces/{workspace}/apps/{app}/versions/{version}").
		To(h.DeleteAppVersion).
		Doc("Delete the specified app template version").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/apps/{app}/versions").
		To(h.ListAppVersions).
		Doc("Get active versions of app, can filter with these fields(version_id, app_id, name, owner, description, package_name, status, type), default return all active app versions").
		Param(webservice.PathParameter("app", "app template id")).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/workspaces/{workspace}/apps/{app}/versions").
		To(h.ListAppVersions).
		Doc("Get active versions of app, can filter with these fields(version_id, app_id, name, owner, description, package_name, status, type), default return all active app versions").
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("app", "app template id")).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	webservice.Route(webservice.GET("/apps/{app}/versions/{version}/package").
		To(h.GetAppVersionPackage).
		Doc("Get packages of version-specific app").
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/workspaces/{workspace}/apps/{app}/versions/{version}/package").
		To(h.GetAppVersionPackage).
		Doc("Get packages of version-specific app").
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.POST("/workspaces/{workspace}/apps/{app}/versions/{version}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateAppVersion).
		Doc("Patch the specified app template version").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/apps/{app}/versions/{version}/files").
		Deprecate().
		To(h.GetAppVersionFiles).
		Doc("Get app template package files").
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.GET("/workspaces/{workspace}/apps/{app}/versions/{version}/files").
		Deprecate().
		To(h.GetAppVersionFiles).
		Doc("Get app template package files").
		Param(webservice.PathParameter("workspace", "workspace")).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.POST("/apps/{app}/versions/{version}/action").
		To(h.AppVersionAction).
		Doc("Perform submit or other operations on app").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("version", "app template version id")).
		Param(webservice.PathParameter("app", "app template id")))

	// application release

	webservice.Route(webservice.GET("/applications").
		To(h.ListAppRls).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}).
		Doc("List all applications within the specified workspace"))

	webservice.Route(webservice.GET("/workspaces/{workspace}/clusters/{cluster}/namespaces/{namespace}/applications").
		To(h.ListAppRls).
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}).
		Doc("List all applications within the specified cluster and workspace").
		Param(webservice.PathParameter("namespace", "namespace")).
		Param(webservice.PathParameter("cluster", "cluster")).
		Param(webservice.PathParameter("workspace", "workspace").Required(true)))

	webservice.Route(webservice.POST("/applications/{application}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateAppRls).
		Doc("Upgrade application").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("application", "the id of the application").Required(true)))

	webservice.Route(webservice.POST("/applications").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateAppRls).
		Doc("Create application").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}))

	webservice.Route(webservice.GET("/applications/{application}").
		To(h.DescribeAppRls).
		Doc("Describe the specified application of the namespace").
		Param(webservice.PathParameter("application", "the id of the application").Required(true)))

	webservice.Route(webservice.DELETE("/applications/{application}").
		To(h.DeleteAppRls).
		Doc("Delete the specified application").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("application", "the id of the application").Required(true)))

	// category
	webservice.Route(webservice.POST("/categories").
		To(h.CreateOrUpdateCategory).
		Doc("Create app template category").
		Param(webservice.PathParameter("app", "app template id")))

	webservice.Route(webservice.DELETE("/categories/{category}").
		To(h.DeleteCategory).
		Doc("Delete the specified category").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("category", "category id")))

	webservice.Route(webservice.PUT("/categories/{category}").
		Consumes(mimePatch...).
		To(h.CreateOrUpdateCategory).
		Doc("Patch the specified category").
		Returns(http.StatusOK, api.StatusOK, errors.Error{}).
		Param(webservice.PathParameter("category", "category id")))

	webservice.Route(webservice.GET("/categories/{category}").
		To(h.DescribeCategory).
		Doc("Describe the specified category").
		Param(webservice.PathParameter("category", "category id")))

	webservice.Route(webservice.GET("/categories").
		To(h.ListCategories).
		Doc("List categories").
		Returns(http.StatusOK, api.StatusOK, models.PageableResponse{}))

	// review
	webservice.Route(webservice.GET("/reviews").
		To(h.ListReviews).
		Doc("Get reviews of version-specific app").
		Returns(http.StatusOK, api.StatusOK, api.ListResult{}))

	c.Add(webservice)

	return nil
}
