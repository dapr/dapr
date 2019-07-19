package components

type ComponentLoader interface {
	LoadComponents() ([]Component, error)
}
