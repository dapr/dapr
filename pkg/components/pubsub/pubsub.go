package pubsub

// PubSub is the interface for message buses
type PubSub interface {
	Init(metadata Metadata) error
	Publish(req *PublishRequest) error
	Subscribe(req SubscribeRequest, handler func(msg *NewMessage) error) error
}
