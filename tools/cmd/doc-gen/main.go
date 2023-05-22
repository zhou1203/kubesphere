/*
Copyright 2019 The KubeSphere Authors.

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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/go-openapi/loads"
	"github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/pkg/errors"
	urlruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/apiserver/runtime"
	"kubesphere.io/kubesphere/pkg/constants"
	clusterkapisv1alpha1 "kubesphere.io/kubesphere/pkg/kapis/cluster/v1alpha1"
	iamv1beta1 "kubesphere.io/kubesphere/pkg/kapis/iam/v1beta1"
	"kubesphere.io/kubesphere/pkg/kapis/oauth"
	operationsv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/operations/v1alpha2"
	resourcesv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha2"
	resourcesv1alpha3 "kubesphere.io/kubesphere/pkg/kapis/resources/v1alpha3"
	tenantv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha2"
	tenantv1alpha3 "kubesphere.io/kubesphere/pkg/kapis/tenant/v1alpha3"
	terminalv1alpha2 "kubesphere.io/kubesphere/pkg/kapis/terminal/v1alpha2"
	"kubesphere.io/kubesphere/pkg/version"
)

var output string

func init() {
	flag.StringVar(&output, "output", "./api/ks-openapi-spec/swagger.json", "--output=./api.json")
}

func main() {
	flag.Parse()
	swaggerSpec := generateSwaggerJson()

	err := validateSpec(swaggerSpec)
	if err != nil {
		klog.Warningf("Swagger specification has errors")
	}
}

func validateSpec(apiSpec []byte) error {

	swaggerDoc, err := loads.Analyzed(apiSpec, "")
	if err != nil {
		return err
	}

	// Attempts to report about all errors
	validate.SetContinueOnErrors(true)

	v := validate.NewSpecValidator(swaggerDoc.Schema(), strfmt.Default)
	result, _ := v.Validate(swaggerDoc)

	if result.HasWarnings() {
		log.Printf("See warnings below:\n")
		for _, desc := range result.Warnings {
			log.Printf("- WARNING: %s\n", desc.Error())
		}

	}
	if result.HasErrors() {
		str := fmt.Sprintf("The swagger spec is invalid against swagger specification %s.\nSee errors below:\n", swaggerDoc.Version())
		for _, desc := range result.Errors {
			str += fmt.Sprintf("- %s\n", desc.Error())
		}
		log.Println(str)
		return errors.New(str)
	}

	return nil
}

func generateSwaggerJson() []byte {

	container := runtime.Container

	urlruntime.Must(oauth.AddToContainer(container, nil, nil, nil, nil, nil, nil))
	urlruntime.Must(clusterkapisv1alpha1.AddToContainer(container, nil))
	urlruntime.Must(iamv1beta1.AddToContainer(container, nil, nil))
	urlruntime.Must(operationsv1alpha2.AddToContainer(container, nil))
	urlruntime.Must(resourcesv1alpha2.AddToContainer(container, nil, "", ""))
	urlruntime.Must(resourcesv1alpha3.AddToContainer(container, nil))
	urlruntime.Must(tenantv1alpha2.AddToContainer(container, nil, nil, nil, nil, nil, nil))
	urlruntime.Must(tenantv1alpha3.AddToContainer(container, nil, nil, nil, nil, nil, nil))
	urlruntime.Must(terminalv1alpha2.AddToContainer(container, nil, nil, nil, nil))

	config := restfulspec.Config{
		WebServices:                   container.RegisteredWebServices(),
		PostBuildSwaggerObjectHandler: enrichSwaggerObject}

	swagger := restfulspec.BuildSwagger(config)
	swagger.Info.Extensions = make(spec.Extensions)
	swagger.Info.Extensions.Add("x-tagGroups", []struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{
		{
			Name: "Authentication",
			Tags: []string{constants.AuthenticationTag},
		},
		{
			Name: "Identity Management",
			Tags: []string{},
		},
		{
			Name: "Access Management",
			Tags: []string{},
		},
		{
			Name: "Multi-tenancy",
			Tags: []string{
				constants.WorkspaceTag,
				constants.NamespaceTag,
				constants.UserResourceTag,
			},
		},
		{
			Name: "Multi-cluster",
			Tags: []string{
				constants.MultiClusterTag,
			},
		},
		{
			Name: "Resources",
			Tags: []string{
				constants.ClusterResourcesTag,
				constants.NamespaceResourcesTag,
			},
		},
		{
			Name: "Other",
			Tags: []string{
				constants.RegistryTag,
				constants.GitTag,
				constants.ToolboxTag,
				constants.TerminalTag,
			},
		},
		{
			Name: "Logging",
			Tags: []string{constants.LogQueryTag},
		},
		{
			Name: "Events",
			Tags: []string{constants.EventsQueryTag},
		},
		{
			Name: "Auditing",
			Tags: []string{constants.AuditingQueryTag},
		},
		{
			Name: "Network",
			Tags: []string{constants.NetworkTopologyTag},
		},
	})

	data, _ := json.MarshalIndent(swagger, "", "  ")
	err := os.WriteFile(output, data, 0644)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("successfully written to %s", output)

	return data
}

func enrichSwaggerObject(swo *spec.Swagger) {
	swo.Info = &spec.Info{
		InfoProps: spec.InfoProps{
			Title:       "KubeSphere",
			Description: "KubeSphere OpenAPI",
			Version:     version.Get().GitVersion,
			Contact: &spec.ContactInfo{
				ContactInfoProps: spec.ContactInfoProps{
					Name:  "KubeSphere",
					URL:   "https://kubesphere.io/",
					Email: "kubesphere@yunify.com",
				},
			},
			License: &spec.License{
				LicenseProps: spec.LicenseProps{
					Name: "Apache 2.0",
					URL:  "https://www.apache.org/licenses/LICENSE-2.0.html",
				},
			},
		},
	}

	// setup security definitions
	swo.SecurityDefinitions = map[string]*spec.SecurityScheme{
		"jwt": spec.APIKeyAuth("Authorization", "header"),
	}
	swo.Security = []map[string][]string{{"jwt": []string{}}}
}
