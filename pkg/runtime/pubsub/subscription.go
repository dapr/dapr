package pubsub

import (
	"fmt"

	"github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type Subscription struct {
	PubsubName      string                         `json:"pubsubname"`
	Topic           string                         `json:"topic"`
	DeadLetterTopic string                         `json:"deadLetterTopic"`
	Metadata        map[string]string              `json:"metadata"`
	Rules           []*Rule                        `json:"rules,omitempty"`
	Scopes          []string                       `json:"scopes"`
	BulkSubscribe   *BulkSubscribe                 `json:"bulkSubscribe"`
	Type            runtime.PubsubSubscriptionType `json:"type"`
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
