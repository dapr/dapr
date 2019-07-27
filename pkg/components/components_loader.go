package components

// ComponentLoader is an interface for returning Actions components
type ComponentLoader interface {
	LoadComponents() ([]Component, error)
}
