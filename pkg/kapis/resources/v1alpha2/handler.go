/*
Copyright 2020 KubeSphere Authors

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

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/emicklei/go-restful/v3"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/components"
	"kubesphere.io/kubesphere/pkg/models/git"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
	"kubesphere.io/kubesphere/pkg/models/kubectl"
	"kubesphere.io/kubesphere/pkg/models/quotas"
	"kubesphere.io/kubesphere/pkg/models/registries"
	resourcev1alpha3 "kubesphere.io/kubesphere/pkg/models/resources/v1alpha3/resource"
	"kubesphere.io/kubesphere/pkg/models/revisions"
	"kubesphere.io/kubesphere/pkg/server/errors"
)

type resourceHandler struct {
	componentsGetter    components.Getter
	resourceQuotaGetter quotas.ResourceQuotaGetter
	revisionGetter      revisions.RevisionGetter
	gitVerifier         git.GitVerifier
	registryGetter      registries.RegistryGetter
	kubeconfigOperator  kubeconfig.Interface
	kubectlOperator     kubectl.Interface
	resourceGetter      *resourcev1alpha3.ResourceGetter
}

func newResourceHandler(cacheClient runtimeclient.Client, masterURL, kubectlImage string) *resourceHandler {
	return &resourceHandler{
		resourceGetter:      resourcev1alpha3.NewResourceGetter(cacheClient),
		componentsGetter:    components.NewComponentsGetter(cacheClient),
		resourceQuotaGetter: quotas.NewResourceQuotaGetter(cacheClient),
		revisionGetter:      revisions.NewRevisionGetter(cacheClient),
		gitVerifier:         git.NewGitVerifier(cacheClient),
		registryGetter:      registries.NewRegistryGetter(cacheClient),
		kubeconfigOperator:  kubeconfig.NewReadOnlyOperator(cacheClient, masterURL),
		kubectlOperator:     kubectl.NewOperator(cacheClient, kubectlImage),
	}
}

func (r *resourceHandler) handleGetSystemHealthStatus(_ *restful.Request, response *restful.Response) {
	result, err := r.componentsGetter.GetSystemHealthStatus()

	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetComponentStatus(request *restful.Request, response *restful.Response) {
	component := request.PathParameter("component")
	result, err := r.componentsGetter.GetComponentStatus(component)

	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetComponents(_ *restful.Request, response *restful.Response) {
	result, err := r.componentsGetter.GetAllComponentsStatus()

	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetClusterQuotas(_ *restful.Request, response *restful.Response) {
	result, err := r.resourceQuotaGetter.GetClusterQuota()
	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetNamespaceQuotas(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	quota, err := r.resourceQuotaGetter.GetNamespaceQuota(namespace)

	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}

	response.WriteAsJson(quota)
}

func (r *resourceHandler) handleGetDaemonSetRevision(request *restful.Request, response *restful.Response) {
	daemonset := request.PathParameter("daemonset")
	namespace := request.PathParameter("namespace")
	revision, err := strconv.Atoi(request.PathParameter("revision"))

	if err != nil {
		response.WriteHeaderAndEntity(http.StatusBadRequest, errors.Wrap(err))
		return
	}

	result, err := r.revisionGetter.GetDaemonSetRevision(namespace, daemonset, revision)

	if err != nil {
		response.WriteHeaderAndEntity(http.StatusInternalServerError, errors.Wrap(err))
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetDeploymentRevision(request *restful.Request, response *restful.Response) {
	deploy := request.PathParameter("deployment")
	namespace := request.PathParameter("namespace")
	revision := request.PathParameter("revision")

	result, err := r.revisionGetter.GetDeploymentRevision(namespace, deploy, revision)
	if err != nil {
		response.WriteHeaderAndEntity(http.StatusInternalServerError, errors.Wrap(err))
		return
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) handleGetStatefulSetRevision(request *restful.Request, response *restful.Response) {
	statefulset := request.PathParameter("statefulset")
	namespace := request.PathParameter("namespace")
	revision, err := strconv.Atoi(request.PathParameter("revision"))
	if err != nil {
		response.WriteHeaderAndEntity(http.StatusBadRequest, errors.Wrap(err))
		return
	}

	result, err := r.revisionGetter.GetStatefulSetRevision(namespace, statefulset, revision)
	if err != nil {
		api.HandleInternalError(response, nil, err)
		return
	}
	response.WriteAsJson(result)
}

func (r *resourceHandler) handleVerifyGitCredential(request *restful.Request, response *restful.Response) {
	var credential api.GitCredential
	err := request.ReadEntity(&credential)
	if err != nil {
		response.WriteHeaderAndEntity(http.StatusInternalServerError, errors.Wrap(err))
		return
	}
	var namespace, secretName string
	if credential.SecretRef != nil {
		namespace = credential.SecretRef.Namespace
		secretName = credential.SecretRef.Name
	}
	err = r.gitVerifier.VerifyGitCredential(credential.RemoteUrl, namespace, secretName)
	if err != nil {
		response.WriteHeaderAndEntity(http.StatusInternalServerError, errors.Wrap(err))
		return
	}
	response.WriteAsJson(errors.None)
}

func (r *resourceHandler) handleVerifyRegistryCredential(request *restful.Request, response *restful.Response) {
	var credential api.RegistryCredential
	err := request.ReadEntity(&credential)
	if err != nil {
		api.HandleBadRequest(response, nil, err)
		return
	}

	err = r.registryGetter.VerifyRegistryCredential(credential)
	if err != nil {
		api.HandleBadRequest(response, nil, err)
		return
	}

	response.WriteHeader(http.StatusOK)
}

func (r *resourceHandler) handleGetRegistryEntry(request *restful.Request, response *restful.Response) {
	imageName := request.QueryParameter("image")
	namespace := request.QueryParameter("namespace")
	secretName := request.QueryParameter("secret")
	insecure := request.QueryParameter("insecure") == "true"

	detail, err := r.registryGetter.GetEntry(namespace, secretName, imageName, insecure)
	if err != nil {
		api.HandleBadRequest(response, nil, err)
		return
	}

	response.WriteAsJson(detail)
}

func (r *resourceHandler) handleGetNamespacedAbnormalWorkloads(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")

	result := api.Workloads{
		Namespace: namespace,
		Count:     make(map[string]int),
	}

	for _, workloadType := range []string{api.ResourceKindDeployment, api.ResourceKindStatefulSet, api.ResourceKindDaemonSet, api.ResourceKindJob, api.ResourceKindPersistentVolumeClaim} {
		var notReadyStatus string

		switch workloadType {
		case api.ResourceKindPersistentVolumeClaim:
			notReadyStatus = strings.Join([]string{"pending", "lost"}, "|")
		case api.ResourceKindJob:
			notReadyStatus = "failed"
		default:
			notReadyStatus = "updating"
		}

		q := query.New()
		q.Filters[query.FieldStatus] = query.Value(notReadyStatus)

		res, err := r.resourceGetter.List(workloadType, namespace, q)
		if err != nil {
			api.HandleInternalError(response, nil, err)
		}

		result.Count[workloadType] = len(res.Items)
	}

	response.WriteAsJson(result)
}

func (r *resourceHandler) GetKubectlPod(request *restful.Request, response *restful.Response) {
	kubectlPod, err := r.kubectlOperator.GetKubectlPod(request.PathParameter("user"))
	if err != nil {
		klog.Errorln(err)
		response.WriteHeaderAndEntity(http.StatusInternalServerError, errors.Wrap(err))
		return
	}
	response.WriteEntity(kubectlPod)
}

func (r *resourceHandler) GetKubeconfig(request *restful.Request, response *restful.Response) {
	user := request.PathParameter("user")

	kubectlConfig, err := r.kubeconfigOperator.GetKubeConfig(user)

	if err != nil {
		klog.Error(err)
		if k8serr.IsNotFound(err) {
			// recreate
			response.WriteHeaderAndJson(http.StatusNotFound, errors.Wrap(err), restful.MIME_JSON)
		} else {
			response.WriteHeaderAndJson(http.StatusInternalServerError, errors.Wrap(err), restful.MIME_JSON)
		}
		return
	}

	response.Write([]byte(kubectlConfig))
}
