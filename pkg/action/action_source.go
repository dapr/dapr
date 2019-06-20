package action

type ActionSource interface {
	Init(eventSourceSpec EventSourceSpec) error
	ReadAsync(metadata interface{}, callback func([]byte) error) error
	Read(metadata interface{}) (interface{}, error)
	Write(data interface{}) error
}
