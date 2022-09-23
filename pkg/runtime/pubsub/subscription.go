package pubsub

import (
	"encoding/json"
	"errors"
	"fmt"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type Subscription struct {
	PubsubName      string            `json:"pubsubname"`
	Topic           string            `json:"topic"`
	DeadLetterTopic string            `json:"deadLetterTopic"`
	Metadata        map[string]string `json:"metadata"`
	Rules           []*Rule           `json:"rules,omitempty"`
	Scopes          []string          `json:"scopes"`
}

// ToProto returns the *commonv1pb.TopicSubscription object that matches this Subscription.
func (r Subscription) ToProto() *runtimev1pb.TopicSubscription {
	rules := make([]*runtimev1pb.TopicRule, len(r.Rules))
	for i, v := range r.Rules {
		rules[i] = v.ToProto()
	}
	return &runtimev1pb.TopicSubscription{
		PubsubName:      r.PubsubName,
		Topic:           r.Topic,
		DeadLetterTopic: r.DeadLetterTopic,
		Metadata:        r.Metadata,
		Routes: &runtimev1pb.TopicRoutes{
			Rules: rules,
		},
	}
}

// NewSubscriptionFromProto returns a new Subscription from a proto object.
func NewSubscriptionFromProto(p *runtimev1pb.TopicSubscription, fallbackRoute string) (*Subscription, error) {
	if p == nil {
		return nil, errors.New("parameter is nil")
	}

	rules, err := ParseRoutingRule(p.Routes, fallbackRoute)
	if err != nil {
		return nil, err
	}

	return &Subscription{
		PubsubName:      p.PubsubName,
		Topic:           p.Topic,
		DeadLetterTopic: p.DeadLetterTopic,
		Metadata:        p.Metadata,
		Rules:           rules,
	}, nil
}

type Rule struct {
	Match Expr   `json:"match"`
	Path  string `json:"path"`
}

// ToProto returns the *commonv1pb.TopicRule object that matches this Rule.
func (r Rule) ToProto() *runtimev1pb.TopicRule {
	var match string
	if r.Match != nil {
		match = r.Match.String()
	}
	return &runtimev1pb.TopicRule{
		Match: match,
		Path:  r.Path,
	}
}

type Expr interface {
	json.Marshaler
	fmt.Stringer
	Eval(variables map[string]interface{}) (interface{}, error)
}

type ActiveSubscription struct {
	PubsubName string `json:"pubsubname"`
	Topic      string `json:"topic"`
}
