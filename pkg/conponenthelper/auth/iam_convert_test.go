package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/yaml"

	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
)

type ObjectPackage struct {
	Objects  []client.Object
	FileName string
}

func ReadObjectFromYamlFiles(path string) (*ObjectPackage, error) {

	split := strings.Split(path, "/")
	fileName := split[len(split)-1]

	objects := make([]client.Object, 0)

	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	roleTemplateYamls := strings.Split(string(file), "---")

	for _, s := range roleTemplateYamls {
		if s == "" {
			continue
		}
		roleTemplate := &iamv1alpha2.RoleBase{}
		err := yaml.Unmarshal([]byte(strings.Trim(s, " ")), roleTemplate)
		if err != nil {
			return nil, err
		}
		objects = append(objects, roleTemplate)
	}

	return &ObjectPackage{
		Objects:  objects,
		FileName: fileName,
	}, nil
}

func WriteObjectToYamlFiles(roPackage *ObjectPackage, convertFn func(client.Object) (client.Object, error)) error {
	myFile, err := os.Create(roPackage.FileName)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(myFile)

	for _, object := range roPackage.Objects {

		newObject, err := convertFn(object)
		if err != nil {
			return err
		}
		if newObject == nil {
			continue
		}

		objectYaml, err := yaml.Marshal(newObject)
		if err != nil {
			return err
		}
		_, err = writer.Write(objectYaml)
		if err != nil {
			return err
		}
		_, err = writer.WriteString("---\n")
		if err != nil {
			return err
		}
		writer.Flush()
	}
	return nil
}

func RoleTemplateConvert(obj client.Object) (client.Object, error) {
	if obj == nil {
		return obj, nil
	}

	template := obj.(*iamv1beta1.RoleTemplate)

	if len(template.Labels) == 0 {
		return template, nil
	}

	if template.Annotations == nil {
		template.Annotations = make(map[string]string, 0)
	} else {
		if template.Annotations["iam.kubesphere.io/role-template-rules"] != "" {
			return template, nil
		}
	}
	resource := template.Labels["iam.kubesphere.io/resource"]
	operation := template.Labels["iam.kubesphere.io/operation"]
	if resource == "" || operation == "" {
		return template, nil
	}
	result := template.DeepCopy()
	delete(result.Labels, "iam.kubesphere.io/resource")
	delete(result.Labels, "iam.kubesphere.io/operation")
	result.Annotations["iam.kubesphere.io/role-template-rules"] = fmt.Sprintf("{\"%s\": \"%s\"}", resource, operation)

	return result, nil
}

func RoleConvert(obj client.Object) (client.Object, error) {
	if obj == nil {
		return nil, nil
	}

	if obj.GetObjectKind().GroupVersionKind().Kind != "RoleBase" {
		return nil, nil
	}

	result := &iamv1beta1.RoleTemplate{
		TypeMeta: v1.TypeMeta{
			APIVersion: "iam.kubesphere.io/v1beta1",
			Kind:       "RoleTemplate",
		},
		ObjectMeta: v1.ObjectMeta{
			Labels:      make(map[string]string, 0),
			Annotations: make(map[string]string, 0),
		},
		Spec: iamv1beta1.RoleTemplateSpec{
			DisplayName: make(map[string]string, 0),
		},
	}
	roleBase := obj.(*iamv1alpha2.RoleBase)
	role := &rbacv1.Role{}
	if !strings.HasPrefix(roleBase.Name, "role-template") {
		return nil, nil
	}

	u := &unstructured.Unstructured{}
	err := yaml.Unmarshal(roleBase.Role.Raw, u)
	if err != nil {
		return nil, err
	}

	if u.GroupVersionKind().Kind != "Role" {
		return nil, nil
	}

	if err := yaml.Unmarshal(roleBase.Role.Raw, role); err != nil {
		return nil, err
	}

	result.Name = strings.Replace(roleBase.Name, "role-template", "namespace", -1)

	category := strings.Replace(strings.Trim(strings.ToLower(role.Annotations["iam.kubesphere.io/module"]), " "), " ", "-", -1)

	if strings.Contains(category, "&") {
		category = strings.Replace(category, "&", "and", 1)
	}

	result.Labels[Category] = category
	result.Labels[IsTemplate] = role.Labels[IsTemplate]
	result.Labels[Scope] = ""

	if role.Annotations[Dependencies] != "" {

		result.Annotations[Dependencies] = strings.Replace(role.Annotations[Dependencies], "role-template", "namespace", -1)
	}
	if role.Annotations[Rule] != "" {
		result.Annotations[Rule] = role.Annotations[Rule]
	}

	result.Spec.DisplayName["en"] = role.Annotations["kubesphere.io/alias-name"]

	result.Spec.Rules = role.Rules

	return result, nil
}

const (
	IsTemplate   = "iam.kubesphere.io/role-template"
	Category     = "iam.kubesphere.io/category"
	Scope        = "scope.iam.kubesphere.io/namespace"
	Dependencies = "iam.kubesphere.io/dependencies"
	Rule         = "iam.kubesphere.io/role-template-rules"
)

func TestRoleTemplateConvert(t *testing.T) {
	roPackage, err := ReadObjectFromYamlFiles("/Users/wenhaozhou/Desktop/RoleTemplate/roletemplate/workspace-roletemplate.yaml")
	if err != nil {
		panic(err)
	}

	err = WriteObjectToYamlFiles(roPackage, RoleTemplateConvert)
	if err != nil {
		panic(err)
	}
}

func TestRoleConvert(t *testing.T) {
	objectPackage, err := ReadObjectFromYamlFiles("/Users/wenhaozhou/Workspace/github/ks-installer/roles/ks-core/prepare/files/ks-init/role-templates.yaml")
	if err != nil {
		panic(err)
	}

	err = WriteObjectToYamlFiles(objectPackage, RoleConvert)
	if err != nil {
		panic(err)
	}
}
