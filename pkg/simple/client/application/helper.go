package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"

	gopkgyaml "gopkg.in/yaml.v2"

	"io"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
)

type VersionRequest struct {
	RepoName    string   `json:"RepoName"`
	Name        string   `json:"Name,omitempty"`
	AppName     string   `json:"appName,omitempty"`
	Home        string   `json:"home,omitempty"`
	Version     string   `json:"version,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Digest      string   `json:"digest,omitempty"`
	Description string   `json:"description,omitempty"`
	Data        []byte
	AppType     string `json:"app_type,omitempty"`
}

type NewAppRequest struct {
	RepoName    string
	AppName     string
	Url         string
	Icon        string
	AppHome     string
	Workspace   string
	Description string
	Credential  appv2.HelmRepoCredential `json:"credential,omitempty"`
}

func CreateOrUpdateApp(ctx context.Context, client client.Client, request NewAppRequest, vRequests []VersionRequest) error {

	app := appv2.Application{}
	app.Name = request.AppName

	_, err := controllerutil.CreateOrUpdate(ctx, client, &app, func() error {
		app.Spec = appv2.ApplicationSpec{
			DisplayName: corev1alpha1.NewLocales(request.AppName, request.AppName),
			Description: corev1alpha1.NewLocales(request.Description, request.Description),
			Icon:        request.Icon,
			AppHome:     request.AppHome,
		}
		app.Labels = map[string]string{
			appv2.RepoIDLabelKey:        request.RepoName,
			constants.WorkspaceLabelKey: request.Workspace,
		}
		return nil
	})
	if err != nil {
		klog.Errorf("failed create or update app %s, err:%v", app.Name, err)
		return err
	}
	app.Status.State = appv2.StatusActive
	now := metav1.Now()
	app.Status.UpdateTime = &now
	err = client.Status().Update(ctx, &app)
	if err != nil {
		return err
	}

	for _, vRequest := range vRequests {
		if err = CreateOrUpdateAppVersion(ctx, client, request, app, vRequest); err != nil {
			return err
		}
	}

	return nil
}

func CreateOrUpdateAppVersion(ctx context.Context, client client.Client, request NewAppRequest, app appv2.Application, vRequest VersionRequest) error {

	//1. create or update app version
	appVersion := appv2.ApplicationVersion{}
	appVersion.Name = vRequest.Name

	mutateFn := func() error {
		if err := controllerutil.SetControllerReference(&app, &appVersion, scheme.Scheme); err != nil {
			klog.Errorf("%s SetControllerReference failed, err:%v", appVersion.Name, err)
			return err
		}

		appVersion.Spec = appv2.ApplicationVersionSpec{
			DisplayName: corev1alpha1.NewLocales(vRequest.AppName, vRequest.AppName),
			Version:     vRequest.Version,
			Home:        vRequest.Home,
			Icon:        vRequest.Icon,
			Description: corev1alpha1.NewLocales(vRequest.Description, vRequest.Description),
			Sources:     vRequest.Sources,
			Created:     &metav1.Time{Time: time.Now()},
			Digest:      vRequest.Digest,
		}

		appVersion.Labels = map[string]string{
			appv2.RepoIDLabelKey:        request.RepoName,
			appv2.AppIDLabelKey:         request.AppName,
			constants.WorkspaceLabelKey: request.Workspace,
		}
		return nil
	}
	_, err := controllerutil.CreateOrUpdate(ctx, client, &appVersion, mutateFn)

	if err != nil {
		klog.Errorf("failed create or update app version %s, err:%v", appVersion.Name, err)
		return err
	}

	//2. create or update configmap
	cm := &corev1.ConfigMap{}
	cm.Name = appVersion.Name
	cm.Namespace = constants.KubeSphereNamespace

	_, err = controllerutil.CreateOrUpdate(ctx, client, cm, func() error {
		if err = controllerutil.SetControllerReference(&appVersion, cm, scheme.Scheme); err != nil {
			klog.Errorf("%s SetControllerReference failed, err:%v", cm.Name, err)
			return err
		}
		cm.BinaryData = map[string][]byte{appv2.BinaryKey: vRequest.Data}
		label := map[string]string{"appType": vRequest.AppType}
		cm.SetLabels(label)
		return nil
	})

	//3. update app version status
	appVersion.Status.State = appv2.StatusActive
	err = client.Status().Update(ctx, &appVersion)

	return err
}

func ReadYaml(data []byte) (jsonList []json.RawMessage) {
	reader := bytes.NewReader(data)
	bufReader := bufio.NewReader(reader)
	r := yaml.NewYAMLReader(bufReader)
	for {
		d, err := r.Read()
		if err != nil && err == io.EOF {
			break
		}
		jsonData, err := yaml.ToJSON(d)
		if err != nil {
			log.Fatalf("Error converting YAML to JSON: %v", err)
		}
		jsonList = append(jsonList, jsonData)
	}
	return jsonList
}

func Decode(data []byte) (obj runtime.Object, gvk *schema.GroupVersionKind, err error) {
	decoder := unstructured.UnstructuredJSONScheme
	obj, gvk, err = decoder.Decode(data, nil, nil)
	return obj, gvk, err
}

func GvkToGvr(gvk *schema.GroupVersionKind, mapper meta.RESTMapper) (schema.GroupVersionResource, error) {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) || err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, nil
}
func GetInfoFromBytes(bytes json.RawMessage, mapper meta.RESTMapper) (gvr schema.GroupVersionResource, utd *unstructured.Unstructured, err error) {
	obj, gvk, err := Decode(bytes)
	if err != nil {
		return gvr, utd, err
	}
	gvr, err = GvkToGvr(gvk, mapper)
	if err != nil {
		return gvr, utd, err
	}
	utd, err = ConvertToUnstructured(obj)
	return gvr, utd, err
}
func ConvertToUnstructured(obj any) (*unstructured.Unstructured, error) {
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	return &unstructured.Unstructured{Object: objMap}, err
}

func ConvertJSONToYAMLList(jsonList []json.RawMessage) ([]byte, error) {
	var yamlBuffer bytes.Buffer

	for i, jsonData := range jsonList {
		var yamlData interface{}
		if err := yaml.Unmarshal(jsonData, &yamlData); err != nil {
			return nil, err
		}
		yamlBytes, err := gopkgyaml.Marshal(yamlData)
		if err != nil {
			return nil, err
		}
		yamlBuffer.Write(yamlBytes)
		if i < len(jsonList)-1 {
			yamlBuffer.WriteString("\n---\n")
		}
	}
	return yamlBuffer.Bytes(), nil
}
