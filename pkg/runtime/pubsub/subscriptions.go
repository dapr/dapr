package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/expr"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/utils"
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
				rule, err := createRoutingRule(r.Match, r.Path)
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

// DeclarativeLocal loads subscriptions from the given local resources path.
func DeclarativeLocal(resourcesPaths []string, namespace string, log logger.Logger) (subs []Subscription) {
	for _, path := range resourcesPaths {
		res := declarativeFile(path, namespace, log)
		if len(res) > 0 {
			subs = append(subs, res...)
		}
	}
	return subs
}

// Used by DeclarativeLocal to load a single path.
func declarativeFile(resourcesPath string, namespace string, log logger.Logger) (subs []Subscription) {
	if _, err := os.Stat(resourcesPath); os.IsNotExist(err) {
		return subs
	}

	files, err := os.ReadDir(resourcesPath)
	if err != nil {
		log.Errorf("Failed to read subscriptions from path %s: %s", resourcesPath, err)
		return subs
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		if !utils.IsYaml(f.Name()) {
			log.Warnf("A non-YAML pubsub file %s was detected, it will not be loaded", f.Name())
			continue
		}

		filePath := filepath.Join(resourcesPath, f.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Warnf("Failed to read file %s: %v", f.Name(), err)
			continue
		}

		bytesArray := bytes.Split(b, []byte("\n---"))
		for _, item := range bytesArray {
			// Skip empty items
			item = bytes.TrimSpace(item)
			if len(item) == 0 {
				continue
			}

			subs, err = appendSubscription(subs, item, namespace)
			if err != nil {
				log.Warnf("Failed to add subscription from file %s: %v", f.Name(), err)
				continue
			}
		}
	}

	return subs
}

func unmarshalSubscription(b []byte, namespace string) (*Subscription, error) {
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
		var sub subscriptionsapiV2alpha1.Subscription
		if err := yaml.Unmarshal(b, &sub); err != nil {
			return nil, err
		}
		if namespace != "" && sub.Namespace != "" && sub.Namespace != namespace {
			return nil, nil
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
			BulkSubscribe: &BulkSubscribe{
				Enabled:            sub.Spec.BulkSubscribe.Enabled,
				MaxMessagesCount:   sub.Spec.BulkSubscribe.MaxMessagesCount,
				MaxAwaitDurationMs: sub.Spec.BulkSubscribe.MaxAwaitDurationMs,
			},
		}, nil

	default:
		// assume "v1alpha1" for backward compatibility as this was
		// not checked before the introduction of "v2alpha".
		var sub subscriptionsapiV1alpha1.Subscription
		if err := yaml.Unmarshal(b, &sub); err != nil {
			return nil, err
		}
		if namespace != "" && sub.Namespace != "" && sub.Namespace != namespace {
			return nil, nil
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
			BulkSubscribe: &BulkSubscribe{
				Enabled:            sub.Spec.BulkSubscribe.Enabled,
				MaxMessagesCount:   sub.Spec.BulkSubscribe.MaxMessagesCount,
				MaxAwaitDurationMs: sub.Spec.BulkSubscribe.MaxAwaitDurationMs,
			},
		}, nil
	}
}

func parseRoutingRulesYAML(routes subscriptionsapiV2alpha1.Routes) ([]*Rule, error) {
	r := make([]*Rule, len(routes.Rules)+1)

	var (
		n   int
		err error
	)
	for _, rule := range routes.Rules {
		r[n], err = createRoutingRule(rule.Match, rule.Path)
		if err != nil {
			return nil, err
		}
		n++
	}

	// If a default path is set, add a rule with a nil `Match`,
	// which is treated as `true` and always selected if
	// no previous rules match.
	if routes.Default != "" {
		r[n] = &Rule{
			Path: routes.Default,
		}
		n++
	}

	return r[:n], nil
}

func parseRoutingRulesGRPC(routes *runtimev1pb.TopicRoutes) ([]*Rule, error) {
	if routes == nil {
		return []*Rule{{
			Path: "",
		}}, nil
	}
	r := make([]*Rule, 0, len(routes.GetRules())+1)

	for _, rule := range routes.GetRules() {
		rr, err := createRoutingRule(rule.GetMatch(), rule.GetPath())
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
func DeclarativeKubernetes(ctx context.Context, client operatorv1pb.OperatorClient, podName string, namespace string, log logger.Logger) []Subscription {
	var subs []Subscription
	resp, err := client.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{
		PodName:   podName,
		Namespace: namespace,
	})
	if err != nil {
		log.Errorf("Failed to list subscriptions from operator: %s", err)
		return subs
	}

	for _, s := range resp.GetSubscriptions() {
		// No namespace filtering here as it's been already filtered by the operator
		subs, err = appendSubscription(subs, s, "")
		if err != nil {
			log.Warnf("Failed to add subscription from operator: %s", err)
			continue
		}
	}

	return subs
}

func appendSubscription(list []Subscription, subBytes []byte, namespace string) ([]Subscription, error) {
	sub, err := unmarshalSubscription(subBytes, namespace)
	if err != nil {
		return nil, err
	}

	if sub != nil {
		list = append(list, *sub)
	}

	return list, nil
}
