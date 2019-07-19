package components

type ComponentHandler interface {
	OnComponentUpdated(component Component)
}
