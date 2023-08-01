package applicationclass

import (
	"fmt"
	"strings"

	"k8s.io/klog/v2"
	"kubesphere.io/api/applicationclass/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

type ProbeEvent struct {
	Plugin     ApplicationPlugin
	PluginName string
}

type ApplicationOptions struct {
	client.Client
	Kubeconfig      string
	AppResourceName string
	App             *v1alpha1.Application //temp
	Parameters      map[string]string
}

type ApplicationPlugin interface {
	Init() error
	GetPluginName() string
}

type ProvisionAppResourcePlugin interface {
	ApplicationPlugin
	NewProvisioner(options ApplicationOptions) (Provisioner, error)
}

type ApplicationPluginMgr struct {
	plugins map[string]ApplicationPlugin
}

func (pm *ApplicationPluginMgr) InitPlugins(plugins []ApplicationPlugin) error {

	if pm.plugins == nil {
		pm.plugins = map[string]ApplicationPlugin{}
	}

	allErrs := []error{}
	for _, plugin := range plugins {
		name := plugin.GetPluginName()
		if errs := validation.IsQualifiedName(name); len(errs) != 0 {
			allErrs = append(allErrs, fmt.Errorf("application plugin has invalid name: %q: %s", name, strings.Join(errs, ";")))
			continue
		}
		if _, found := pm.plugins[name]; found {
			allErrs = append(allErrs, fmt.Errorf("application plugin %q was registered more than once", name))
			continue
		}
		pm.plugins[name] = plugin
		klog.V(1).InfoS("Loaded application class plugin", "pluginName", name)
	}
	return utilerrors.NewAggregate(allErrs)
}

func (pm *ApplicationPluginMgr) FindPluginByName(name string) (ApplicationPlugin, error) {

	var match ApplicationPlugin
	if v, found := pm.plugins[name]; found {
		match = v
	}

	if match == nil {
		return nil, fmt.Errorf("no application class plugin matched name: %s", name)
	}

	return match, nil
}

func (pm *ApplicationPluginMgr) FindProvisionablePluginByName(name string) (ProvisionAppResourcePlugin, error) {
	applicationPlugin, err := pm.FindPluginByName(name)
	if err != nil {
		return nil, err
	}
	if provisionPlugin, ok := applicationPlugin.(ProvisionAppResourcePlugin); ok {
		return provisionPlugin, nil
	}
	return nil, fmt.Errorf("no provisionable application plugin matched")
}
