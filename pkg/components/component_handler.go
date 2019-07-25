package components

// ComponentHandler is an interface for reacting on component changes
type ComponentHandler interface {
	OnComponentUpdated(component Component)
}
