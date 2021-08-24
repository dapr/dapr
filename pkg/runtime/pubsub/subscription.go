package pubsub

type dlq struct {
	IsBrokerSpecific bool   `json:"isbrokerspecific,omitempty"`
	Pubsubname       string `json:"pubsubname,omitempty"`
	Topic            string `json:"topic,omitempty"`
}

type Subscription struct {
	PubsubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Route      string            `json:"route"`
	Metadata   map[string]string `json:"metadata"`
	Scopes     []string          `json:"scopes"`
	DLQ        dlq               `json:"dlq,omitempty"`
}
