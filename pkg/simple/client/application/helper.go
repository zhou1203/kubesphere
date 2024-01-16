package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/labels"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	appv2 "kubesphere.io/api/application/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
)

type AppRequest struct {
	RepoName     string               `json:"repoName,omitempty"`
	AppName      string               `json:"appName,omitempty"`
	AliasName    string               `json:"aliasName,omitempty"`
	VersionName  string               `json:"versionName,omitempty"`
	AppHome      string               `json:"appHome,omitempty"`
	Url          string               `json:"url,omitempty"`
	Icon         string               `json:"icon,omitempty"`
	Digest       string               `json:"digest,omitempty"`
	Workspace    string               `json:"workspace,omitempty"`
	Description  string               `json:"description,omitempty"`
	CategoryName string               `json:"categoryName,omitempty"`
	AppType      string               `json:"appType,omitempty"`
	Package      []byte               `json:"package,omitempty"`
	Credential   appv2.RepoCredential `json:"credential,omitempty"`
	Maintainers  []appv2.Maintainer   `json:"maintainers,omitempty"`
	Abstraction  string               `json:"abstraction,omitempty"`
	Attachments  []string             `json:"attachments,omitempty"`
}

func CreateOrUpdateApp(ctx context.Context, client runtimeclient.Client, request AppRequest, vRequests []AppRequest) error {

	app := appv2.Application{}
	app.Name = request.AppName

	operationResult, err := controllerutil.CreateOrUpdate(ctx, client, &app, func() error {
		app.Spec = appv2.ApplicationSpec{
			Icon:        request.Icon,
			AppHome:     request.AppHome,
			AppType:     request.AppType,
			Abstraction: request.Abstraction,
			Attachments: request.Attachments,
		}
		labels := app.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[appv2.RepoIDLabelKey] = request.RepoName
		labels[appv2.AppTypeLabelKey] = request.AppType

		if request.CategoryName != "" {
			labels[appv2.AppCategoryNameKey] = request.CategoryName
		} else {
			labels[appv2.AppCategoryNameKey] = appv2.UncategorizedCategoryID
		}

		labels[constants.WorkspaceLabelKey] = request.Workspace
		app.SetLabels(labels)

		ant := app.GetAnnotations()
		if ant == nil {
			ant = make(map[string]string)
		}
		ant[constants.DisplayNameAnnotationKey] = request.AliasName
		ant[constants.DescriptionAnnotationKey] = request.Description
		if len(request.Maintainers) > 0 {
			ant[appv2.AppMaintainersKey] = request.Maintainers[0].Name
		}
		app.SetAnnotations(ant)

		return nil
	})
	if err != nil {
		klog.Errorf("failed create or update app %s, err:%v", app.Name, err)
		return err
	}

	if operationResult == controllerutil.OperationResultCreated {
		app.Status.State = appv2.ReviewStatusDraft
	}
	app.Status.UpdateTime = &metav1.Time{Time: time.Now()}
	if err = client.Status().Update(ctx, &app); err != nil {
		return err
	}

	for _, vRequest := range vRequests {
		if err = CreateOrUpdateAppVersion(ctx, client, app, vRequest); err != nil {
			return err
		}
	}

	return nil
}

func CreateOrUpdateAppVersion(ctx context.Context, client runtimeclient.Client, app appv2.Application, vRequest AppRequest) error {

	//1. create or update app version
	appVersion := appv2.ApplicationVersion{}
	appVersion.Name = fmt.Sprintf("%s-%s-%s", vRequest.RepoName, app.Name, vRequest.VersionName)

	mutateFn := func() error {
		if err := controllerutil.SetControllerReference(&app, &appVersion, scheme.Scheme); err != nil {
			klog.Errorf("%s SetControllerReference failed, err:%v", appVersion.Name, err)
			return err
		}
		appVersion.Spec = appv2.ApplicationVersionSpec{
			VersionName: vRequest.VersionName,
			AppHome:     vRequest.AppHome,
			Icon:        vRequest.Icon,
			Created:     &metav1.Time{Time: time.Now()},
			Digest:      vRequest.Digest,
			AppType:     vRequest.AppType,
			Maintainer:  vRequest.Maintainers,
		}

		labels := appVersion.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[appv2.RepoIDLabelKey] = vRequest.RepoName
		labels[appv2.AppIDLabelKey] = vRequest.AppName
		labels[appv2.AppTypeLabelKey] = vRequest.AppType
		labels[constants.WorkspaceLabelKey] = vRequest.Workspace
		appVersion.SetLabels(labels)

		ant := appVersion.GetAnnotations()
		if ant == nil {
			ant = make(map[string]string)
		}
		ant[constants.DisplayNameAnnotationKey] = vRequest.AliasName
		ant[constants.DescriptionAnnotationKey] = vRequest.Description
		if len(vRequest.Maintainers) > 0 {
			ant[appv2.AppMaintainersKey] = vRequest.Maintainers[0].Name
		}
		appVersion.SetAnnotations(ant)
		return nil
	}
	operationResult, err := controllerutil.CreateOrUpdate(ctx, client, &appVersion, mutateFn)

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
		cm.BinaryData = map[string][]byte{appv2.BinaryKey: vRequest.Package}
		label := map[string]string{"appType": vRequest.AppType}
		cm.SetLabels(label)
		return nil
	})

	//3. update app version status
	if operationResult == controllerutil.OperationResultCreated {
		appVersion.Status.State = appv2.ReviewStatusDraft
	}
	appVersion.Status.Updated = &metav1.Time{Time: time.Now()}
	err = client.Status().Update(ctx, &appVersion)

	//4. update app latest version
	appVersionList := appv2.ApplicationVersionList{}
	lbs := labels.SelectorFromSet(labels.Set{appv2.AppIDLabelKey: app.Name})
	opt := runtimeclient.ListOptions{LabelSelector: lbs}
	err = client.List(ctx, &appVersionList, &opt)
	if err != nil {
		return err
	}

	latestAppVersion := "0.0.0"
	for _, v := range appVersionList.Items {
		parsedVersion, err := semver.Make(strings.TrimPrefix(v.Spec.VersionName, "v"))
		if err != nil {
			klog.Info("Failed to parse version: ", v.Spec.VersionName)
			continue
		}
		if parsedVersion.GT(semver.MustParse(strings.TrimPrefix(latestAppVersion, "v"))) {
			latestAppVersion = v.Spec.VersionName
		}
	}

	ant := app.GetAnnotations()
	ant[appv2.LatestAppVersionKey] = latestAppVersion
	app.SetAnnotations(ant)
	err = client.Update(ctx, &app)

	return err
}

func ReadYaml(data []byte) (jsonList []json.RawMessage, err error) {
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
			return nil, err
		}
		_, _, err = Decode(jsonData)
		if err != nil {
			return nil, err
		}
		jsonList = append(jsonList, jsonData)
	}
	return jsonList, nil
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

func ComplianceCheck(values, tempLate []byte, mapper meta.RESTMapper, ns string) (result []json.RawMessage, err error) {
	yamlList, err := ReadYaml(values)
	if err != nil {
		return nil, err
	}
	yamlTempList, err := ReadYaml(tempLate)
	if err != nil {
		return nil, err
	}

	if len(yamlTempList) != len(yamlList) {
		return nil, errors.New("yamlList and yamlTempList length not equal")
	}
	for idx := range yamlTempList {
		_, utd, err := GetInfoFromBytes(yamlList[idx], mapper)
		if err != nil {
			return nil, err
		}
		_, utdTemp, err := GetInfoFromBytes(yamlTempList[idx], mapper)
		if err != nil {
			return nil, err
		}
		if utdTemp.GetKind() != utd.GetKind() || utdTemp.GetAPIVersion() != utd.GetAPIVersion() {
			return nil, errors.New("yamlList and yamlTempList not equal")
		}
		if utd.GetNamespace() != ns {
			return nil, errors.New("subresource must have same namespace with app release")
		}
	}
	return yamlList, nil
}
