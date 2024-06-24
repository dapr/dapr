package pubsub

import "fmt"

type SubscriptionType int32

const (
	SubscriptionTypeUnknown      = 0
	SubscriptionTypeDeclarative  = 1
	SubscriptionTypeProgrammatic = 2
	SubscriptionTypeStreaming    = 3
)

var subscriptionTypeName = map[int32]string{
	0: "Unknown",
	1: "Declarative",
	2: "Programmatic",
	3: "Streaming",
}

func (s SubscriptionType) String() string {
	return subscriptionTypeName[int32(s)]
}

type Subscription struct {
	PubsubName      string            `json:"pubsubname"`
	Topic           string            `json:"topic"`
	DeadLetterTopic string            `json:"deadLetterTopic"`
	Metadata        map[string]string `json:"metadata"`
	Rules           []*Rule           `json:"rules,omitempty"`
	Scopes          []string          `json:"scopes"`
	BulkSubscribe   *BulkSubscribe    `json:"bulkSubscribe"`
	Type            SubscriptionType
}

type BulkSubscribe struct {
	Enabled            bool  `json:"enabled"`
	MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
}

type Rule struct {
	Match Expr   `json:"match"`
	Path  string `json:"path"`
}

type Expr interface {
	fmt.Stringer

	Eval(variables map[string]interface{}) (interface{}, error)
}
