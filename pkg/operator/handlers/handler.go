package handlers

// Handler is the interface for dealing with Dapr CRDs state changes.
type Handler interface {
	Init() error
}
