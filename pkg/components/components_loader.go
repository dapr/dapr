package components

import components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"

// ComponentLoader is an interface for returning Actions components
type ComponentLoader interface {
	LoadComponents() ([]components_v1alpha1.Component, error)
}
