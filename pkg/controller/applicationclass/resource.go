package applicationclass

import (
	"context"

	"kubesphere.io/api/applicationclass/v1alpha1"
)

// Provisioner create the application resource in the provider.
type Provisioner interface {
	Provision(context.Context) (*v1alpha1.ApplicationResource, error)
	Uninstall(context.Context) error
}
