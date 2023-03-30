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

package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/meta"
	urlruntime "k8s.io/apimachinery/pkg/util/runtime"

	kubespec "k8s.io/kube-openapi/pkg/validation/spec"
	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/version"
	"kubesphere.io/kubesphere/tools/lib"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/kube-openapi/pkg/common"

	tenantv1alpha1 "kubesphere.io/api/tenant/v1alpha1"
)

var output string

func init() {
	flag.StringVar(&output, "output", "./api/openapi-spec/swagger.json", "--output=./api/openapi-spec/swagger.json")
}

func main() {

	var (
		Scheme = runtime.NewScheme()
		Codecs = serializer.NewCodecFactory(Scheme)
	)

	urlruntime.Must(clusterv1alpha1.AddToScheme(Scheme))
	urlruntime.Must(Scheme.SetVersionPriority(clusterv1alpha1.SchemeGroupVersion))

	mapper := meta.NewDefaultRESTMapper(nil)

	mapper.AddSpecific(tenantv1alpha1.SchemeGroupVersion.WithKind(tenantv1alpha1.ResourceKindWorkspace),
		tenantv1alpha1.SchemeGroupVersion.WithResource(tenantv1alpha1.ResourcePluralWorkspace),
		tenantv1alpha1.SchemeGroupVersion.WithResource(tenantv1alpha1.ResourceSingularWorkspace), meta.RESTScopeRoot)

	mapper.AddSpecific(clusterv1alpha1.SchemeGroupVersion.WithKind(clusterv1alpha1.ResourceKindCluster),
		clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesPluralCluster),
		clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesSingularCluster), meta.RESTScopeRoot)

	mapper.AddSpecific(clusterv1alpha1.SchemeGroupVersion.WithKind(clusterv1alpha1.ResourceKindCluster),
		clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesPluralCluster),
		clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesSingularCluster), meta.RESTScopeRoot)

	spec, err := lib.RenderOpenAPISpec(lib.Config{
		Scheme: Scheme,
		Codecs: Codecs,
		Info: kubespec.InfoProps{
			Title:   "KubeSphere",
			Version: version.Get().GitVersion,
			Contact: &kubespec.ContactInfo{
				Name:  "KubeSphere",
				URL:   "https://kubesphere.io/",
				Email: "kubesphere@yunify.com",
			},
			License: &kubespec.License{
				Name: "Apache 2.0",
				URL:  "https://www.apache.org/licenses/LICENSE-2.0.html",
			},
		},
		OpenAPIDefinitions: []common.GetOpenAPIDefinitions{
			tenantv1alpha1.GetOpenAPIDefinitions,
			clusterv1alpha1.GetOpenAPIDefinitions,
		},
		Resources: []schema.GroupVersionResource{
			//TODO（runzexia） At present, the document generation requires the openapi structure of the go language,
			// but there is no +k8s:openapi-gen=true in the repository of https://github.com/knative/pkg,
			// and the api document cannot be generated temporarily.
			tenantv1alpha1.SchemeGroupVersion.WithResource(tenantv1alpha1.ResourcePluralWorkspace),
			clusterv1alpha1.SchemeGroupVersion.WithResource(clusterv1alpha1.ResourcesPluralCluster),
		},
		Mapper: mapper,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = os.MkdirAll(filepath.Dir(output), 0755)
	if err != nil {
		log.Fatal(err)
	}
	err = os.WriteFile(output, []byte(spec), 0644)
	if err != nil {
		log.Fatal(err)
	}
}
