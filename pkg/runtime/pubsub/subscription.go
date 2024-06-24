package pubsub

import "fmt"

type SubscriptionType int32

const (
	SubscriptionTypeUnknown = iota
	SubscriptionTypeDeclarative
	SubscriptionTypeProgrammatic
	SubscriptionTypeStreaming
)

var subscriptionTypeName = map[int32]string{
	SubscriptionTypeUnknown:      "Unknown",
	SubscriptionTypeDeclarative:  "Declarative",
	SubscriptionTypeProgrammatic: "Programmatic",
	SubscriptionTypeStreaming:    "Streaming",
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
	Type            SubscriptionType  `json:"type"`
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
