package components

// Type is the component type.
type Type string

const (
	State          Type = "state"
	PubSub         Type = "pubsub"
	InputBinding   Type = "inputbinding"
	OutputBinding  Type = "outputbinding"
	HTTPMiddleware Type = "middleware.http"
	Configuration  Type = "configuration"
	Secret         Type = "secret"
	Lock           Type = "lock"
	NameResolution Type = "nameresolution"
)
