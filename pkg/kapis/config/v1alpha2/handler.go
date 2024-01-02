package v1alpha2

import (
	"github.com/emicklei/go-restful/v3"
	"github.com/mitchellh/mapstructure"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/apiserver/config"
	"kubesphere.io/kubesphere/pkg/apiserver/rest"
	"kubesphere.io/kubesphere/pkg/constants"
)

const (
	themeConfigurationName = "platform-configuration-theme"
)

type handler struct {
	config *config.Config
	client client.Client
}

func NewHandler(config *config.Config, client client.Client) rest.Handler {
	return &handler{config: config, client: client}
}

type ThemeConfiguration struct {
	Title       string `json:"title" yaml:"title" mapstructure:"title"`
	Description string `json:"description" yaml:"description" mapstructure:"description"`
	Logo        string `json:"logo" yaml:"logo" mapstructure:"logo"`
	Favicon     string `json:"favicon" yaml:"favicon" mapstructure:"favicon"`
	Background  string `json:"background" yaml:"background" mapstructure:"background"`
}

func (h *handler) updateThemeConfiguration(req *restful.Request, resp *restful.Response) {
	platformInformation := ThemeConfiguration{}
	if err := req.ReadEntity(&platformInformation); err != nil {
		api.HandleBadRequest(resp, req, err)
		return
	}
	var data map[string]string
	if err := mapstructure.Decode(platformInformation, &data); err != nil {
		api.HandleBadRequest(resp, req, err)
		return
	}
	configMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      themeConfigurationName,
			Namespace: constants.KubeSphereNamespace,
		},
	}
	_, err := ctrl.CreateOrUpdate(req.Request.Context(), h.client, &configMap, func() error {
		configMap.Data = data
		return nil
	})
	if err != nil {
		api.HandleInternalError(resp, req, err)
		return
	}
	_ = resp.WriteEntity(platformInformation)
}

func (h *handler) getThemeConfiguration(req *restful.Request, resp *restful.Response) {
	var configMap corev1.ConfigMap
	themeConfiguration := &ThemeConfiguration{}
	configName := types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: themeConfigurationName}
	if err := h.client.Get(req.Request.Context(), configName, &configMap); err != nil {
		if apierrors.IsNotFound(err) {
			_ = resp.WriteEntity(themeConfiguration)
			return
		}
		api.HandleInternalError(resp, req, err)
		return
	}
	if err := mapstructure.Decode(configMap.Data, themeConfiguration); err != nil {
		klog.Warningf("failed to decode theme configuration: %v", err)
	}
	_ = resp.WriteEntity(themeConfiguration)
}
