package installer

import (
	"context"
	"encoding/json"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime/schema"

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
	Mapper      meta.RESTMapper
	DynamicCli  *dynamic.DynamicClient
	GvrListInfo []InsInfo
	Namespace   string
}
type InsInfo struct {
	schema.GroupVersionResource
	Name      string
	Namespace string
}

var _ helm.Executor = &YamlInstaller{}

func (t YamlInstaller) Install(ctx context.Context, chartName string, chartData, values []byte, options ...helm.HelmOption) (string, error) {
	return "", nil
}

func (t YamlInstaller) Upgrade(ctx context.Context, chartName string, tempLate, values []byte, options ...helm.HelmOption) (string, error) {
	yamlList, err := application.ComplianceCheck(values, tempLate, t.Mapper, t.Namespace)
	if err != nil {
		return "", err
	}
	klog.Infof("attempting to apply %d yaml files", len(yamlList))

	err = t.ForApply(yamlList)

	return "", err
}

func (t YamlInstaller) Uninstall(ctx context.Context, options ...helm.HelmOption) (string, error) {
	for _, i := range t.GvrListInfo {
		err := t.DynamicCli.Resource(i.GroupVersionResource).Namespace(i.Namespace).
			Delete(ctx, i.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (t YamlInstaller) ForceDelete(ctx context.Context, options ...helm.HelmOption) error {
	_, err := t.Uninstall(ctx, options...)
	return err
}

func (t YamlInstaller) Release(options ...helm.HelmOption) (*helmrelease.Release, error) {
	rv := &helmrelease.Release{}
	rv.Info = &helmrelease.Info{Status: helmrelease.StatusDeployed}
	return rv, nil
}

func (t YamlInstaller) IsReleaseReady(timeout time.Duration, options ...helm.HelmOption) (bool, error) {
	return true, nil
}

func (t YamlInstaller) ForApply(tasks []json.RawMessage) (err error) {

	for idx, js := range tasks {

		gvr, utd, err := application.GetInfoFromBytes(js, t.Mapper)
		if err != nil {
			return err
		}
		opt := metav1.PatchOptions{FieldManager: "v1.FieldManager"}
		_, err = t.DynamicCli.Resource(gvr).
			Namespace(utd.GetNamespace()).
			Patch(context.TODO(), utd.GetName(), types.ApplyPatchType, js, opt)

		if err != nil {
			return err
		}
		klog.Infof("[%d/%d] %s/%s applied", idx+1, len(tasks), gvr.Resource, utd.GetName())
	}
	return nil
}
