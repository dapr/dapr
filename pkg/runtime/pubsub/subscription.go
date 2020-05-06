package pubsub

type Subscription struct {
	Topic string `json:"topic"`
	Route string `json:"route"`
	Metadata map[string]string `json:"metadata"`
}
