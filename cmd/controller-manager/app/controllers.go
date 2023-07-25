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

package app

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"kubesphere.io/kubesphere/cmd/controller-manager/app/options"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	"kubesphere.io/kubesphere/pkg/controller/certificatesigningrequest"
	"kubesphere.io/kubesphere/pkg/controller/cluster"
	"kubesphere.io/kubesphere/pkg/controller/clusterrole"
	"kubesphere.io/kubesphere/pkg/controller/core"
	"kubesphere.io/kubesphere/pkg/controller/extension"
	"kubesphere.io/kubesphere/pkg/controller/globalrole"
	"kubesphere.io/kubesphere/pkg/controller/globalrolebinding"
	"kubesphere.io/kubesphere/pkg/controller/group"
	"kubesphere.io/kubesphere/pkg/controller/groupbinding"
	"kubesphere.io/kubesphere/pkg/controller/job"
	"kubesphere.io/kubesphere/pkg/controller/ksserviceaccount"
	"kubesphere.io/kubesphere/pkg/controller/loginrecord"
	"kubesphere.io/kubesphere/pkg/controller/marketplace"
	"kubesphere.io/kubesphere/pkg/controller/namespace"
	"kubesphere.io/kubesphere/pkg/controller/quota"
	"kubesphere.io/kubesphere/pkg/controller/role"
	"kubesphere.io/kubesphere/pkg/controller/roletemplate"
	"kubesphere.io/kubesphere/pkg/controller/secret"
	"kubesphere.io/kubesphere/pkg/controller/serviceaccount"
	"kubesphere.io/kubesphere/pkg/controller/storage/capability"
	"kubesphere.io/kubesphere/pkg/controller/telemetry"
	"kubesphere.io/kubesphere/pkg/controller/user"
	"kubesphere.io/kubesphere/pkg/controller/workspace"
	"kubesphere.io/kubesphere/pkg/controller/workspacerole"
	"kubesphere.io/kubesphere/pkg/controller/workspacerolebinding"
	"kubesphere.io/kubesphere/pkg/controller/workspacetemplate"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
	"kubesphere.io/kubesphere/pkg/utils/clusterclient"
)

var allControllers = []string{
	"user",
	"workspacetemplate",
	"workspace",
	"workspacerole",
	"workspacerolebinding",
	"namespace",
	"application",
	"serviceaccount",
	"resourcequota",
	"job",
	"storagecapability",
	"loginrecord",
	"cluster",
	"nsnp",
	"ippool",
	"csr",
	"globalrole",
	"globalrolebinding",
	"roletemplate",
	"clusterrole",
	"role",
	"groupbinding",
	"group",
	"rulegroup",
	"clusterrulegroup",
	"globalrulegroup",
	"repository",
	"installplan",
	"extension",
	"extension-webhook",
	"ks-serviceaccount",
	"secret",
	"marketplace",
}

// setup all available controllers one by one
func addAllControllers(mgr manager.Manager, client k8s.Client, cmOptions *options.KubeSphereControllerManagerOptions) error {
	// begin init controller and add to manager one by one
	// init controllers for host cluster
	if err := addHostControllers(mgr, client, cmOptions); err != nil {
		return err
	}

	// "workspace" controller
	if cmOptions.IsControllerEnabled("workspace") {
		addControllerWithSetup(mgr, "workspace", &workspace.Reconciler{})
	}

	// "namespace" controller
	if cmOptions.IsControllerEnabled("namespace") {
		namespaceReconciler := &namespace.Reconciler{}
		addControllerWithSetup(mgr, "namespace", namespaceReconciler)
	}

	// "serviceaccount" controller
	if cmOptions.IsControllerEnabled("serviceaccount") {
		saReconciler := &serviceaccount.Reconciler{}
		addControllerWithSetup(mgr, "serviceaccount", saReconciler)
	}

	// "resourcequota" controller
	if cmOptions.IsControllerEnabled("resourcequota") {
		resourceQuotaReconciler := &quota.Reconciler{
			MaxConcurrentReconciles: quota.DefaultMaxConcurrentReconciles,
			ResyncPeriod:            quota.DefaultResyncPeriod,
		}
		addControllerWithSetup(mgr, "resourcequota", resourceQuotaReconciler)
	}

	// "job" controller
	if cmOptions.IsControllerEnabled("job") {
		addControllerWithSetup(mgr, "job", &job.Reconciler{})
	}

	// "storagecapability" controller
	if cmOptions.IsControllerEnabled("storagecapability") {
		addControllerWithSetup(mgr, "storagecapability", &capability.Reconciler{})
	}

	// "loginrecord" controller
	if cmOptions.IsControllerEnabled("loginrecord") {
		loginRecordController := loginrecord.NewReconciler(
			cmOptions.AuthenticationOptions.LoginHistoryRetentionPeriod, cmOptions.AuthenticationOptions.LoginHistoryMaximumEntries,
		)
		addControllerWithSetup(mgr, "loginrecord", loginRecordController)
	}

	// "csr" controller
	if cmOptions.IsControllerEnabled("csr") {
		csrController := certificatesigningrequest.NewReconciler(client.Config())
		addControllerWithSetup(mgr, "csr", csrController)
	}

	if cmOptions.IsControllerEnabled("clusterrole") {
		clusterRoleController := &clusterrole.Reconciler{}
		addControllerWithSetup(mgr, "clusterrole", clusterRoleController)
	}

	if cmOptions.IsControllerEnabled("role") {
		roleController := &role.Reconciler{}
		addControllerWithSetup(mgr, "role", roleController)
	}

	if cmOptions.IsControllerEnabled("roletemplate") {
		roletemplateController := &roletemplate.Reconciler{}
		addControllerWithSetup(mgr, "roletemplate", roletemplateController)
	}

	// "groupbinding" controller
	if cmOptions.IsControllerEnabled("groupbinding") {
		addControllerWithSetup(mgr, "groupbinding", &groupbinding.Reconciler{})
	}

	// "group" controller
	if cmOptions.IsControllerEnabled("group") {
		addControllerWithSetup(mgr, "group", &group.Reconciler{})
	}

	// "serviceaccount" controller
	if cmOptions.IsControllerEnabled("ks-serviceaccount") {
		addControllerWithSetup(mgr, "ks-serviceaccount", &ksserviceaccount.Reconciler{})
	}

	// "secret" controller
	issuer, err := token.NewIssuer(cmOptions.AuthenticationOptions)
	if err != nil {
		return err
	}
	if cmOptions.IsControllerEnabled("secret") {
		addControllerWithSetup(mgr, "secret", &secret.Reconciler{TokenIssuer: issuer})
	}

	// log all controllers process result
	for _, name := range allControllers {
		if cmOptions.IsControllerEnabled(name) {
			if addSuccessfullyControllers.Has(name) {
				klog.Infof("%s controller is enabled and added successfully.", name)
			} else {
				klog.Infof("%s controller is enabled but is not going to run due to its dependent component being disabled.", name)
			}
		} else {
			klog.Infof("%s controller is disabled by controller selectors.", name)
		}
	}

	return nil
}

func addHostControllers(mgr manager.Manager, client k8s.Client, cmOptions *options.KubeSphereControllerManagerOptions) error {
	if cmOptions.MultiClusterOptions.ClusterRole != multicluster.ClusterRoleHost {
		return nil
	}

	clusterClientSet, err := clusterclient.NewClusterClientSet(mgr.GetCache())
	if err != nil {
		return err
	}

	// "user" controller
	if cmOptions.IsControllerEnabled("user") {
		userController := &user.Reconciler{
			AuthenticationOptions: cmOptions.AuthenticationOptions,
			ClusterClientSet:      clusterClientSet,
		}
		addControllerWithSetup(mgr, "user", userController)
	}

	var k8sVersion string
	if cmOptions.IsControllerEnabled("extension") {
		info, err := client.Kubernetes().Discovery().ServerVersion()
		if err == nil {
			k8sVersion = info.GitVersion
		} else {
			return err
		}
		extensionReconciler := &core.ExtensionReconciler{K8sVersion: k8sVersion}
		addControllerWithSetup(mgr, "extension", extensionReconciler)

		extensionVersionReconciler := &core.ExtensionVersionReconciler{K8sVersion: k8sVersion}
		addControllerWithSetup(mgr, "extensionversion", extensionVersionReconciler)
	}

	// extension webhook
	if cmOptions.IsControllerEnabled("extension-webhook") {
		addControllerWithSetup(mgr, "extension-webhook", &extension.JSBundleWebhook{})
		addControllerWithSetup(mgr, "extension-webhook", &extension.APIServiceWebhook{})
		addControllerWithSetup(mgr, "extension-webhook", &extension.ReverseProxyWebhook{})
	}

	if cmOptions.IsControllerEnabled("repository") {
		repoReconciler := &core.RepositoryReconciler{}
		addControllerWithSetup(mgr, "repository", repoReconciler)
	}

	if cmOptions.IsControllerEnabled("installplan") {
		installPlanReconciler, err := core.NewInstallPlanReconciler(cmOptions.KubernetesOptions.KubeConfig)
		if err != nil {
			return fmt.Errorf("failed to create installplan controller: %v", err)
		}
		addControllerWithSetup(mgr, "installplan", installPlanReconciler)
	}

	// marketplace controller
	if cmOptions.IsControllerEnabled("marketplace") {
		addControllerWithSetup(mgr, "marketplace", &marketplace.Controller{})
	}

	// "workspacetemplate" controller
	if cmOptions.IsControllerEnabled("workspacetemplate") {
		addControllerWithSetup(mgr, "workspacetemplate", &workspacetemplate.Reconciler{ClusterClientSet: clusterClientSet})
	}

	// "workspacerole" controller
	if cmOptions.IsControllerEnabled("workspacerole") {
		addControllerWithSetup(mgr, "workspacerole", &workspacerole.Reconciler{ClusterClientSet: clusterClientSet})
	}

	// "workspacerolebinding" controller
	if cmOptions.IsControllerEnabled("workspacerolebinding") {
		addControllerWithSetup(mgr, "workspacerolebinding", &workspacerolebinding.Reconciler{ClusterClientSet: clusterClientSet})
	}

	// "globalrole" controller
	if cmOptions.IsControllerEnabled("globalrole") {
		addControllerWithSetup(mgr, "globalrole", &globalrole.Reconciler{ClusterClientSet: clusterClientSet})
	}

	// "globalrolebinding" controller
	if cmOptions.IsControllerEnabled("globalrolebinding") {
		addControllerWithSetup(mgr, "globalrolebinding", &globalrolebinding.Reconciler{ClusterClientSet: clusterClientSet})
	}

	// "cluster" controller
	if cmOptions.IsControllerEnabled("cluster") {
		clusterReconciler, err := cluster.NewReconciler(client.Config(), cmOptions.MultiClusterOptions.HostClusterName, cmOptions.MultiClusterOptions.ClusterControllerResyncPeriod)
		if err != nil {
			klog.Fatalf("Unable to create Cluster controller: %v", err)
		}
		// Register reconciler
		addControllerWithSetup(mgr, "cluster", clusterReconciler)
		// Register timed tasker
		addController(mgr, "cluster", clusterReconciler)
	}

	if cmOptions.TelemetryOptions != nil && cmOptions.TelemetryOptions.Enabled != nil && *cmOptions.TelemetryOptions.Enabled {
		addControllerWithSetup(mgr, "telemetry", &telemetry.Reconciler{Options: cmOptions.TelemetryOptions})
	}
	return nil
}

var addSuccessfullyControllers = sets.New[string]()

type setupableController interface {
	SetupWithManager(mgr ctrl.Manager) error
}

func addControllerWithSetup(mgr manager.Manager, name string, controller setupableController) {
	if err := controller.SetupWithManager(mgr); err != nil {
		klog.Fatalf("Unable to create %v controller: %v", name, err)
	}
	addSuccessfullyControllers.Insert(name)
}

func addController(mgr manager.Manager, name string, controller manager.Runnable) {
	if err := mgr.Add(controller); err != nil {
		klog.Fatalf("Unable to create %v controller: %v", name, err)
	}
	addSuccessfullyControllers.Insert(name)
}
