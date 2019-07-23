package channel

type AppChannel interface {
	InvokeMethod(req *InvokeRequest) (*InvokeResponse, error)
}
