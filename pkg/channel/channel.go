package channel

// AppChannel is an abstrdapr over communications with user code
type AppChannel interface {
	InvokeMethod(req *InvokeRequest) (*InvokeResponse, error)
}
