package installer

import (
	"context"
	"encoding/json"
	"errors"

	helmrelease "helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/simple/client/application"

	"k8s.io/client-go/dynamic"
	"kubesphere.io/utils/helm"

	"time"
)

type YamlInstaller struct {
	Mapper          meta.RESTMapper
	DynamicCli      *dynamic.DynamicClient
	AppRlsReference metav1.OwnerReference
}

var _ helm.Executor = &YamlInstaller{}

func (t YamlInstaller) Install(ctx context.Context, chartName string, chartData, values []byte, options ...helm.HelmOption) (string, error) {
	return "", nil
}

func (t YamlInstaller) Upgrade(ctx context.Context, chartName string, tempLate, values []byte, options ...helm.HelmOption) (string, error) {
	yamlList, err := t.complianceCheck(values, tempLate)
	if err != nil {
		return "", err
	}
	klog.Infof("attempting to apply %d yaml files", len(yamlList))

	var references []metav1.OwnerReference
	references = append(references, t.AppRlsReference)
	err = t.ForApply(yamlList, references)

	return "", err
}

func (t YamlInstaller) complianceCheck(values, tempLate []byte) ([]json.RawMessage, error) {
	yamlList, yamlTempList := application.ReadYaml(values), application.ReadYaml(tempLate)

	if len(yamlTempList) != len(yamlList) {
		return nil, errors.New("yamlList and yamlTempList length not equal")
	}
	for idx := range yamlTempList {
		_, utd, err := application.GetInfoFromBytes(yamlList[idx], t.Mapper)
		if err != nil {
			return nil, err
		}
		_, utdTemp, err := application.GetInfoFromBytes(yamlTempList[idx], t.Mapper)
		if err != nil {
			return nil, err
		}
		if utdTemp.GetKind() != utd.GetKind() || utdTemp.GetAPIVersion() != utd.GetAPIVersion() {
			return nil, errors.New("yamlList and yamlTempList not equal")
		}
	}
	return yamlList, nil
}

func (t YamlInstaller) Uninstall(ctx context.Context, options ...helm.HelmOption) (string, error) {
	return "", nil
}

func (t YamlInstaller) ForceDelete(ctx context.Context, options ...helm.HelmOption) error {
	return nil
}

func (t YamlInstaller) Release(options ...helm.HelmOption) (*helmrelease.Release, error) {
	rv := &helmrelease.Release{}
	rv.Info = &helmrelease.Info{Status: helmrelease.StatusDeployed}
	return rv, nil
}

func (t YamlInstaller) IsReleaseReady(timeout time.Duration, options ...helm.HelmOption) (bool, error) {
	return true, nil
}

func (t YamlInstaller) ForApply(tasks []json.RawMessage, references []metav1.OwnerReference) (err error) {

	for idx, js := range tasks {

		gvr, utd, err := application.GetInfoFromBytes(js, t.Mapper)
		if err != nil {
			return err
		}
		opt := metav1.PatchOptions{FieldManager: "v1.FieldManager"}

		utd.SetOwnerReferences(references)

		marshalJSON, err := utd.MarshalJSON()
		if err != nil {
			return err
		}

		_, err = t.DynamicCli.Resource(gvr).
			Namespace(utd.GetNamespace()).
			Patch(context.TODO(), utd.GetName(), types.ApplyPatchType, marshalJSON, opt)

		if err != nil {
			return err
		}
		klog.Infof("[%d/%d] %s/%s applied", idx+1, len(tasks), gvr.Resource, utd.GetName())
	}
	return nil
}
