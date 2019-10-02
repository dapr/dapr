package handlers

// Handler is the interface for dealing with Dapr CRDs state changes
type Handler interface {
	Init() error
	ObjectCreated(obj interface{})
	ObjectUpdated(old interface{}, new interface{})
	ObjectDeleted(obj interface{})
}
