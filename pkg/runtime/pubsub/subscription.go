package pubsub

type Subscription struct {
	PubsubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Metadata   map[string]string `json:"metadata"`
	Rules      []*Rule           `json:"rules,omitempty"`
	Scopes     []string          `json:"scopes"`
}

type Rule struct {
	Match Expr   `json:"match"`
	Path  string `json:"path"`
}

type Expr interface {
	Eval(variables map[string]interface{}) (interface{}, error)
}
