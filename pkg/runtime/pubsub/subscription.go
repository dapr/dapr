package pubsub

type Subscription struct {
	PubsubName      string            `json:"pubsubname"`
	Topic           string            `json:"topic"`
	DeadLetterTopic string            `json:"deadLetterTopic"`
	Metadata        map[string]string `json:"metadata"`
	Rules           []*Rule           `json:"rules,omitempty"`
	Scopes          []string          `json:"scopes"`
	BulkSubscribe   *BulkSubscribe    `json:"bulkSubscribe"`
}

type BulkSubscribe struct {
	Enabled            string `json:"enabled"`
	MaxMessagesCount   string `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs string `json:"maxAwaitDurationMs,omitempty"`
}

type Rule struct {
	Match Expr   `json:"match"`
	Path  string `json:"path"`
}

type Expr interface {
	Eval(variables map[string]interface{}) (interface{}, error)
}
