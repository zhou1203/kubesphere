package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +genclient:nonNamesapced
// +kubebuidler:object:root=true
// +kubebuilder:printcolumn:name="TemplateScope",type="string",JSONPath=".templateScope"
// +kubebuilder:resource:categories="iam",scope="Cluster"
// +kubebuilder:object:generate=true
type RoleTemplate struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RoleTemplateSpec `json:"spec"`
}

type RoleTemplateSpec struct {
	// +kubebuilder:validation:Enum=global;cluster;workspace;namespace
	TemplateScope string `json:"templateScope"`

	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:EmbeddedResource
	Role runtime.RawExtension `json:"role"`
}

// +kubebuilder:object:root=true
// +kubebuilder:object:generate=true
type RoleTemplateList struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []RoleTemplate `json:"items"`
}
