package components

import components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"

// ComponentHandler is an interface for reacting on component changes
type ComponentHandler interface {
	OnComponentUpdated(component components_v1alpha1.Component)
}
