package handlers

type Handler interface {
	Init() error
	ObjectCreated(obj interface{})
	ObjectUpdated(old interface{}, new interface{})
	ObjectDeleted(obj interface{})
}
