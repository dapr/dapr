package pubsub

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapr/dapr/utils"

	"github.com/cenkalti/backoff/v4"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/kit/retry"

	subscriptionsapi_v1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapi_v2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/expr"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
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
	}

	RoutesJSON struct {
		Rules   []*RuleJSON `json:"rules,omitempty"`
		Default string      `json:"default,omitempty"`
	}

	RuleJSON struct {
		Match string `json:"match"`
		Path  string `json:"path"`
	}
)

func GetSubscriptionsHTTP(channel channel.AppChannel, log logger.Logger, r resiliency.Provider, resiliencyEnabled bool) ([]Subscription, error) {
	var subscriptions []Subscription
	var subscriptionItems []SubscriptionJSON

	req := invokev1.NewInvokeMethodRequest("dapr/subscribe")
	req.WithHTTPExtension(http.MethodGet, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate Context
	ctx := context.Background()

	var resp *invokev1.InvokeMethodResponse
	var err error

	// TODO: Use only resiliency once it is no longer a preview feature.
	if resiliencyEnabled {
		policy := r.BuiltInPolicy(ctx, resiliency.BuiltInInitializationRetries)
		err = policy(func(ctx context.Context) (rErr error) {
			resp, rErr = channel.InvokeMethod(ctx, req)
			return rErr
		})
	} else {
		backoff := getSubscriptionsBackoff()
		retry.NotifyRecover(func() error {
			resp, err = channel.InvokeMethod(ctx, req)
			return err
		}, backoff, func(err error, d time.Duration) {
			log.Debug("failed getting http subscriptions, starting retry")
		}, func() {})
	}

	if err != nil {
		return nil, err
	}

	switch resp.Status().Code {
	case http.StatusOK:
		_, body := resp.RawData()
		if err := json.Unmarshal(body, &subscriptionItems); err != nil {
			log.Errorf(deserializeTopicsError, err)

			return nil, errors.Errorf(deserializeTopicsError, err)
		}
		subscriptions = make([]Subscription, len(subscriptionItems))
		for i, si := range subscriptionItems {
			// Look for single route field and append it as a route struct.
			// This preserves backward compatibility.

			rules := make([]*Rule, 0, len(si.Routes.Rules)+1)
			for _, r := range si.Routes.Rules {
				rule, err := createRoutingRule(r.Match, r.Path)
				if err != nil {
					return nil, err
				}
				rules = append(rules, rule)
			}

			// If a default path is set, add a rule with a nil `Match`,
			// which is treated as `true` and always selected if
			// no previous rules match.
			if si.Routes.Default != "" {
				rules = append(rules, &Rule{
					Path: si.Routes.Default,
				})
			} else if si.Route != "" {
				rules = append(rules, &Rule{
					Path: si.Route,
				})
			}

			subscriptions[i] = Subscription{
				PubsubName:      si.PubsubName,
				Topic:           si.Topic,
				Metadata:        si.Metadata,
				DeadLetterTopic: si.DeadLetterTopic,
				Rules:           rules,
			}
		}

	case http.StatusNotFound:
		log.Debug(noSubscriptionsError)

	default:
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf("app returned http status code %v from subscription endpoint", resp.Status().Code)
	}

	log.Debugf("app responded with subscriptions %v", subscriptions)

	return filterSubscriptions(subscriptions, log), nil
}

func filterSubscriptions(subscriptions []Subscription, log logger.Logger) []Subscription {
	for i := len(subscriptions) - 1; i >= 0; i-- {
		if len(subscriptions[i].Rules) == 0 {
			log.Warnf("topic %s has an empty routes. removing from subscriptions list", subscriptions[i].Topic)
			subscriptions = append(subscriptions[:i], subscriptions[i+1:]...)
		}
	}

	return subscriptions
}

func getSubscriptionsBackoff() backoff.BackOff {
	config := retry.DefaultConfig()
	config.MaxRetries = 3
	config.Duration = time.Second * 2
	config.MaxElapsedTime = time.Second * 10
	config.Policy = retry.PolicyExponential
	return config.NewBackOff()
}

func GetSubscriptionsGRPC(channel runtimev1pb.AppCallbackClient, log logger.Logger, r resiliency.Provider, resiliencyEnabled bool) ([]Subscription, error) {
	var subscriptions []Subscription

	var err error
	var resp *runtimev1pb.ListTopicSubscriptionsResponse

	// TODO: Use only resiliency once it is no longer a preview feature.
	if resiliencyEnabled {
		policy := r.BuiltInPolicy(context.Background(), resiliency.BuiltInInitializationRetries)
		err = policy(func(ctx context.Context) (rErr error) {
			resp, rErr = channel.ListTopicSubscriptions(context.Background(), &emptypb.Empty{})

			if rErr != nil {
				if s, ok := status.FromError(rErr); ok && s != nil {
					if s.Code() == codes.Unimplemented {
						return nil
					}
				}
			}
			return rErr
		})
	} else {
		backoff := getSubscriptionsBackoff()

		retry.NotifyRecover(func() error {
			resp, err = channel.ListTopicSubscriptions(context.Background(), &emptypb.Empty{})

			if err != nil {
				if s, ok := status.FromError(err); ok && s != nil {
					if s.Code() == codes.Unimplemented {
						return nil
					}
				}
			}
			return err
		}, backoff, func(err error, d time.Duration) {
			log.Debug("failed getting gRPC subscriptions, starting retry")
		}, func() {})
	}

	if err != nil {
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf(getTopicsError, err)
		return nil, err
	}

	if resp == nil || resp.Subscriptions == nil || len(resp.Subscriptions) == 0 {
		log.Debug(noSubscriptionsError)
	} else {
		for _, s := range resp.Subscriptions {
			rules, err := parseRoutingRulesGRPC(s.Routes)
			if err != nil {
				return nil, err
			}
			subscriptions = append(subscriptions, Subscription{
				PubsubName:      s.PubsubName,
				Topic:           s.GetTopic(),
				Metadata:        s.GetMetadata(),
				DeadLetterTopic: s.DeadLetterTopic,
				Rules:           rules,
			})
		}
	}

	return subscriptions, nil
}

// DeclarativeSelfHosted loads subscriptions from the given components path.
func DeclarativeSelfHosted(componentsPath string, log logger.Logger) []Subscription {
	var subs []Subscription

	if _, err := os.Stat(componentsPath); os.IsNotExist(err) {
		return subs
	}

	files, err := os.ReadDir(componentsPath)
	if err != nil {
		log.Errorf("failed to read subscriptions from path %s: %s", err)
		return subs
	}

	for _, f := range files {
		if !f.IsDir() {
			if !utils.IsYaml(f.Name()) {
				log.Warnf("A non-YAML pubsub file %s was detected, it will not be loaded", f.Name())
				continue
			}
			filePath := filepath.Join(componentsPath, f.Name())
			b, err := os.ReadFile(filePath)
			if err != nil {
				log.Warnf("failed to read file %s: %s", filePath, err)
				continue
			}

			subs, err = appendSubscription(subs, b)
			if err != nil {
				log.Warnf("failed to add subscription from file %s: %s", filePath, err)
				continue
			}
		}
	}

	return subs
}

func marshalSubscription(b []byte) (*Subscription, error) {
	// Parse only the type metadata first in order
	// to filter out non-Subscriptions without other errors.
	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	var ti typeInfo
	if err := yaml.Unmarshal(b, &ti); err != nil {
		return nil, err
	}

	if ti.Kind != subscriptionKind {
		return nil, nil
	}

	switch ti.APIVersion {
	case APIVersionV2alpha1:
		// "v2alpha1" is the CRD that introduces pubsub routing.
		var sub subscriptionsapi_v2alpha1.Subscription
		if err := yaml.Unmarshal(b, &sub); err != nil {
			return nil, err
		}

		rules, err := parseRoutingRulesYAML(sub.Spec.Routes)
		if err != nil {
			return nil, err
		}

		return &Subscription{
			Topic:           sub.Spec.Topic,
			PubsubName:      sub.Spec.Pubsubname,
			Rules:           rules,
			Metadata:        sub.Spec.Metadata,
			Scopes:          sub.Scopes,
			DeadLetterTopic: sub.Spec.DeadLetterTopic,
		}, nil

	default:
		// assume "v1alpha1" for backward compatibility as this was
		// not checked before the introduction of "v2alpha".
		var sub subscriptionsapi_v1alpha1.Subscription
		if err := yaml.Unmarshal(b, &sub); err != nil {
			return nil, err
		}

		return &Subscription{
			Topic:      sub.Spec.Topic,
			PubsubName: sub.Spec.Pubsubname,
			Rules: []*Rule{
				{
					Path: sub.Spec.Route,
				},
			},
			Metadata:        sub.Spec.Metadata,
			Scopes:          sub.Scopes,
			DeadLetterTopic: sub.Spec.DeadLetterTopic,
		}, nil
	}
}

func parseRoutingRulesYAML(routes subscriptionsapi_v2alpha1.Routes) ([]*Rule, error) {
	r := make([]*Rule, 0, len(routes.Rules)+1)

	for _, rule := range routes.Rules {
		rr, err := createRoutingRule(rule.Match, rule.Path)
		if err != nil {
			return nil, err
		}
		r = append(r, rr)
	}

	// If a default path is set, add a rule with a nil `Match`,
	// which is treated as `true` and always selected if
	// no previous rules match.
	if routes.Default != "" {
		r = append(r, &Rule{
			Path: routes.Default,
		})
	}

	return r, nil
}

func parseRoutingRulesGRPC(routes *runtimev1pb.TopicRoutes) ([]*Rule, error) {
	if routes == nil {
		return []*Rule{{
			Path: "",
		}}, nil
	}
	r := make([]*Rule, 0, len(routes.Rules)+1)

	for _, rule := range routes.Rules {
		rr, err := createRoutingRule(rule.Match, rule.Path)
		if err != nil {
			return nil, err
		}
		r = append(r, rr)
	}

	// If a default path is set, add a rule with a nil `Match`,
	// which is treated as `true` and always selected if
	// no previous rules match.
	if routes.Default != "" {
		r = append(r, &Rule{
			Path: routes.Default,
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

func createRoutingRule(match, path string) (*Rule, error) {
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

// DeclarativeKubernetes loads subscriptions from the operator when running in Kubernetes.
func DeclarativeKubernetes(client operatorv1pb.OperatorClient, podName string, namespace string, log logger.Logger) []Subscription {
	var subs []Subscription
	resp, err := client.ListSubscriptionsV2(context.TODO(), &operatorv1pb.ListSubscriptionsRequest{
		PodName:   podName,
		Namespace: namespace,
	})
	if err != nil {
		log.Errorf("failed to list subscriptions from operator: %s", err)

		return subs
	}

	for _, s := range resp.Subscriptions {
		subs, err = appendSubscription(subs, s)
		if err != nil {
			log.Warnf("failed to add subscription from operator: %s", err)
			continue
		}
	}

	return subs
}

func appendSubscription(list []Subscription, subBytes []byte) ([]Subscription, error) {
	sub, err := marshalSubscription(subBytes)
	if err != nil {
		return nil, err
	}

	if sub != nil {
		list = append(list, *sub)
	}

	return list, nil
}
