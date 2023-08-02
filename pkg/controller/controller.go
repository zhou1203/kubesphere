package controller

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	SyncFailed            = "SyncFailed"
	Synced                = "Synced"
	MessageResourceSynced = "Synced successfully"
)

type Controller interface {
	SetupWithManager(mgr ctrl.Manager) error
}

var _ error = &FailedToSetupError{}

type FailedToSetupError struct {
	controllerName string
	reason         interface{}
}

func FailedToSetup(controllerName string, reason interface{}) error {
	return &FailedToSetupError{controllerName: controllerName, reason: reason}
}

func (e *FailedToSetupError) Error() string {
	return fmt.Sprintf("failed to setup %s: %v", e.controllerName, e.reason)
}
