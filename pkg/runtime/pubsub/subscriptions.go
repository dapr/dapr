package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/expr"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

const (
	getTopicsError         = "error getting topic list from app: %s"
	deserializeTopicsError = "error getting topics from app: %s"
	noSubscriptionsError   = "user app did not subscribe to any topic"
	subscriptionKind       = "Subscription"

	APIVersionV1alpha1 = "dapr.io/v1alpha1"
	APIVersionV2alpha1 = "dapr.io/v2alpha1"
)

type (
	SubscriptionJSON struct {
		PubsubName      string            `json:"pubsubname"`
		Topic           string            `json:"topic"`
		DeadLetterTopic string            `json:"deadLetterTopic"`
		Metadata        map[string]string `json:"metadata,omitempty"`
		Route           string            `json:"route"`  // Single route from v1alpha1
		Routes          RoutesJSON        `json:"routes"` // Multiple routes from v2alpha1
		BulkSubscribe   BulkSubscribeJSON `json:"bulkSubscribe,omitempty"`
	}

	RoutesJSON struct {
		Rules   []*RuleJSON `json:"rules,omitempty"`
		Default string      `json:"default,omitempty"`
	}

	BulkSubscribeJSON struct {
		Enabled            bool  `json:"enabled"`
		MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
		MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
	}

	RuleJSON struct {
		Match string `json:"match"`
		Path  string `json:"path"`
	}
)

func GetSubscriptionsHTTP(ctx context.Context, channel channel.AppChannel, log logger.Logger, r resiliency.Provider) ([]Subscription, error) {
	req := invokev1.NewInvokeMethodRequest("dapr/subscribe").
		WithHTTPExtension(http.MethodGet, "").
		WithContentType(invokev1.JSONContentType)
	defer req.Close()

	policyDef := r.BuiltInPolicy(resiliency.BuiltInInitializationRetries)
	if policyDef != nil && policyDef.HasRetries() {
		req.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return channel.InvokeMethod(ctx, req, "")
	})
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	var (
		subscriptions     []Subscription
		subscriptionItems []SubscriptionJSON
	)

	switch resp.Status().GetCode() {
	case http.StatusOK:
		err = json.NewDecoder(resp.RawData()).Decode(&subscriptionItems)
		if err != nil {
			err = fmt.Errorf(deserializeTopicsError, err)
			log.Error(err)
			return nil, err
		}
		subscriptions = make([]Subscription, len(subscriptionItems))
		for i, si := range subscriptionItems {
			// Look for single route field and append it as a route struct.
			// This preserves backward compatibility.

			rules := make([]*Rule, len(si.Routes.Rules)+1)
			n := 0
			for _, r := range si.Routes.Rules {
				rule, err := CreateRoutingRule(r.Match, r.Path)
				if err != nil {
					return nil, err
				}
				rules[n] = rule
				n++
			}

			// If a default path is set, add a rule with a nil `Match`,
			// which is treated as `true` and always selected if
			// no previous rules match.
			if si.Routes.Default != "" {
				rules[n] = &Rule{
					Path: si.Routes.Default,
				}
				n++
			} else if si.Route != "" {
				rules[n] = &Rule{
					Path: si.Route,
				}
				n++
			}
			bulkSubscribe := &BulkSubscribe{
				Enabled:            si.BulkSubscribe.Enabled,
				MaxMessagesCount:   si.BulkSubscribe.MaxMessagesCount,
				MaxAwaitDurationMs: si.BulkSubscribe.MaxAwaitDurationMs,
			}
			subscriptions[i] = Subscription{
				PubsubName:      si.PubsubName,
				Topic:           si.Topic,
				Metadata:        si.Metadata,
				DeadLetterTopic: si.DeadLetterTopic,
				Rules:           rules[:n],
				BulkSubscribe:   bulkSubscribe,
			}
		}

	case http.StatusNotFound:
		log.Debug(noSubscriptionsError)

	default:
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf("app returned http status code %v from subscription endpoint", resp.Status().GetCode())
	}

	log.Debugf("app responded with subscriptions %v", subscriptions)
	return filterSubscriptions(subscriptions, log), nil
}

func filterSubscriptions(subscriptions []Subscription, log logger.Logger) []Subscription {
	i := 0
	for _, s := range subscriptions {
		if len(s.Rules) == 0 {
			log.Warnf("topic %s has an empty routes. removing from subscriptions list", s.Topic)
			continue
		}
		subscriptions[i] = s
		i++
	}
	return subscriptions[:i]
}

func GetSubscriptionsGRPC(ctx context.Context, channel runtimev1pb.AppCallbackClient, log logger.Logger, r resiliency.Provider) ([]Subscription, error) {
	policyRunner := resiliency.NewRunner[*runtimev1pb.ListTopicSubscriptionsResponse](ctx,
		r.BuiltInPolicy(resiliency.BuiltInInitializationRetries),
	)
	resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
		rResp, rErr := channel.ListTopicSubscriptions(ctx, &emptypb.Empty{})

		if rErr != nil {
			s, ok := status.FromError(rErr)
			if ok && s != nil {
				if s.Code() == codes.Unimplemented {
					log.Infof("pubsub subscriptions: gRPC app does not implement ListTopicSubscriptions")
					return new(runtimev1pb.ListTopicSubscriptionsResponse), nil
				}
			}
		}
		return rResp, rErr
	})
	if err != nil {
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf(getTopicsError, err)
		return nil, err
	}

	var subscriptions []Subscription
	if len(resp.GetSubscriptions()) == 0 {
		log.Debug(noSubscriptionsError)
	} else {
		subscriptions = make([]Subscription, len(resp.GetSubscriptions()))
		for i, s := range resp.GetSubscriptions() {
			rules, err := parseRoutingRulesGRPC(s.GetRoutes())
			if err != nil {
				return nil, err
			}
			var bulkSubscribe *BulkSubscribe
			if s.GetBulkSubscribe() != nil {
				bulkSubscribe = &BulkSubscribe{
					Enabled:            s.GetBulkSubscribe().GetEnabled(),
					MaxMessagesCount:   s.GetBulkSubscribe().GetMaxMessagesCount(),
					MaxAwaitDurationMs: s.GetBulkSubscribe().GetMaxAwaitDurationMs(),
				}
			}
			subscriptions[i] = Subscription{
				PubsubName:      s.GetPubsubName(),
				Topic:           s.GetTopic(),
				Metadata:        s.GetMetadata(),
				DeadLetterTopic: s.GetDeadLetterTopic(),
				Rules:           rules,
				BulkSubscribe:   bulkSubscribe,
			}
		}
	}

	return subscriptions, nil
}

func parseRoutingRulesGRPC(routes *runtimev1pb.TopicRoutes) ([]*Rule, error) {
	if routes == nil {
		return []*Rule{{
			Path: "",
		}}, nil
	}
	r := make([]*Rule, 0, len(routes.GetRules())+1)

	for _, rule := range routes.GetRules() {
		rr, err := CreateRoutingRule(rule.GetMatch(), rule.GetPath())
		if err != nil {
			return nil, err
		}
		r = append(r, rr)
	}

	// If a default path is set, add a rule with a nil `Match`,
	// which is treated as `true` and always selected if
	// no previous rules match.
	if routes.GetDefault() != "" {
		r = append(r, &Rule{
			Path: routes.GetDefault(),
		})
	}

	// gRPC automatically specifies a default route
	// if none are returned.
	if len(r) == 0 {
		r = append(r, &Rule{
			Path: "",
		})
	}

	return r, nil
}

func CreateRoutingRule(match, path string) (*Rule, error) {
	var e *expr.Expr
	matchTrimmed := strings.TrimSpace(match)
	if matchTrimmed != "" {
		e = &expr.Expr{}
		if err := e.DecodeString(matchTrimmed); err != nil {
			return nil, err
		}
	}

	return &Rule{
		Match: e,
		Path:  path,
	}, nil
}
