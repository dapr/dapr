package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/expr"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/kit/logger"
)

const (
	getTopicsError         = "error getting topic list from app: %s"
	deserializeTopicsError = "error getting topics from app: %s"
	noSubscriptionsError   = "user app did not subscribe to any topic"
	subscriptionKind       = "Subscription"

	APIVersionV1alpha1 = "dapr.io/v1alpha1"
	APIVersionV2alpha1 = "dapr.io/v2alpha1"

	MetadataKeyPubSub = "pubsubName"
)

var (
	// errUnexpectedEnvelopeData denotes that an unexpected data type was
	// encountered when processing a cloud event's data property.
	errUnexpectedEnvelopeData = errors.New("unexpected data type encountered in envelope")

	cloudEventDuplicateKeys = sets.NewString(
		contribpubsub.IDField,
		contribpubsub.SourceField,
		contribpubsub.DataContentTypeField,
		contribpubsub.TypeField,
		contribpubsub.SpecVersionField,
		contribpubsub.DataField,
		contribpubsub.DataBase64Field,
	)
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

	SubscribedMessage struct {
		CloudEvent map[string]interface{}
		Data       []byte
		Topic      string
		Metadata   map[string]string
		Path       string
		PubSub     string
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

func GRPCEnvelopeFromSubscriptionMessage(ctx context.Context, msg *SubscribedMessage, log logger.Logger, tracingSpec *config.TracingSpec) (*runtimev1pb.TopicEventRequest, trace.Span, error) {
	cloudEvent := msg.CloudEvent

	envelope := &runtimev1pb.TopicEventRequest{
		Id:              ExtractCloudEventProperty(cloudEvent, contribpubsub.IDField),
		Source:          ExtractCloudEventProperty(cloudEvent, contribpubsub.SourceField),
		DataContentType: ExtractCloudEventProperty(cloudEvent, contribpubsub.DataContentTypeField),
		Type:            ExtractCloudEventProperty(cloudEvent, contribpubsub.TypeField),
		SpecVersion:     ExtractCloudEventProperty(cloudEvent, contribpubsub.SpecVersionField),
		Topic:           msg.Topic,
		PubsubName:      msg.Metadata[MetadataKeyPubSub],
		Path:            msg.Path,
	}

	if data, ok := cloudEvent[contribpubsub.DataBase64Field]; ok && data != nil {
		if dataAsString, ok := data.(string); ok {
			decoded, decodeErr := base64.StdEncoding.DecodeString(dataAsString)
			if decodeErr != nil {
				log.Debugf("unable to base64 decode cloudEvent field data_base64: %s", decodeErr)
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, 0)

				return nil, nil, fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(decodeErr))
			}

			envelope.Data = decoded
		} else {
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, 0)
			return nil, nil, fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(errUnexpectedEnvelopeData))
		}
	} else if data, ok := cloudEvent[contribpubsub.DataField]; ok && data != nil {
		envelope.Data = nil

		if contenttype.IsStringContentType(envelope.GetDataContentType()) {
			switch v := data.(type) {
			case string:
				envelope.Data = []byte(v)
			case []byte:
				envelope.Data = v
			default:
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, 0)
				return nil, nil, fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(errUnexpectedEnvelopeData))
			}
		} else if contenttype.IsJSONContentType(envelope.GetDataContentType()) || contenttype.IsCloudEventContentType(envelope.GetDataContentType()) {
			envelope.Data, _ = json.Marshal(data)
		}
	}

	var span trace.Span
	iTraceID := cloudEvent[contribpubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[contribpubsub.TraceIDField]
	}
	if iTraceID != nil {
		if traceID, ok := iTraceID.(string); ok {
			sc, _ := diag.SpanContextFromW3CString(traceID)
			spanName := fmt.Sprintf("pubsub/%s", msg.Topic)

			// no ops if trace is off
			ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, tracingSpec)
			// span is nil if tracing is disabled (sampling rate is 0)
			if span != nil {
				ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
			}
		} else {
			log.Warnf("ignored non-string traceid value: %v", iTraceID)
		}
	}

	extensions, extensionsErr := ExtractCloudEventExtensions(cloudEvent)
	if extensionsErr != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, 0)
		return nil, nil, extensionsErr
	}
	envelope.Extensions = extensions

	return envelope, span, nil
}

func ExtractCloudEventProperty(cloudEvent map[string]any, property string) string {
	if cloudEvent == nil {
		return ""
	}
	iValue, ok := cloudEvent[property]
	if ok {
		if value, ok := iValue.(string); ok {
			return value
		}
	}

	return ""
}

func ExtractCloudEventExtensions(cloudEvent map[string]any) (*structpb.Struct, error) {
	// Assemble Cloud Event Extensions:
	// Create copy of the cloud event with duplicated data removed

	extensions := make(map[string]any)
	for key, value := range cloudEvent {
		if !cloudEventDuplicateKeys.Has(key) {
			extensions[key] = value
		}
	}
	extensionsStruct := structpb.Struct{}
	extensionBytes, jsonMarshalErr := json.Marshal(extensions)
	if jsonMarshalErr != nil {
		return &extensionsStruct, fmt.Errorf("error processing internal cloud event data: unable to marshal cloudEvent extensions: %s", jsonMarshalErr)
	}

	protoUnmarshalErr := protojson.Unmarshal(extensionBytes, &extensionsStruct)
	if protoUnmarshalErr != nil {
		return &extensionsStruct, fmt.Errorf("error processing internal cloud event data: unable to unmarshal cloudEvent extensions to proto struct: %s", protoUnmarshalErr)
	}
	return &extensionsStruct, nil
}

func FetchEntry(rawPayload bool, entry *contribpubsub.BulkMessageEntry, cloudEvent map[string]interface{}) (*runtimev1pb.TopicEventBulkRequestEntry, error) {
	if rawPayload {
		return &runtimev1pb.TopicEventBulkRequestEntry{
			EntryId:     entry.EntryId,
			Event:       &runtimev1pb.TopicEventBulkRequestEntry_Bytes{Bytes: entry.Event}, //nolint:nosnakecase
			ContentType: entry.ContentType,
			Metadata:    entry.Metadata,
		}, nil
	} else {
		eventLocal, err := extractCloudEvent(cloudEvent)
		if err != nil {
			return nil, err
		}
		return &runtimev1pb.TopicEventBulkRequestEntry{
			EntryId:     entry.EntryId,
			Event:       &eventLocal,
			ContentType: entry.ContentType,
			Metadata:    entry.Metadata,
		}, nil
	}
}

func extractCloudEvent(event map[string]interface{}) (runtimev1pb.TopicEventBulkRequestEntry_CloudEvent, error) { //nolint:nosnakecase
	envelope := &runtimev1pb.TopicEventCERequest{
		Id:              ExtractCloudEventProperty(event, contribpubsub.IDField),
		Source:          ExtractCloudEventProperty(event, contribpubsub.SourceField),
		DataContentType: ExtractCloudEventProperty(event, contribpubsub.DataContentTypeField),
		Type:            ExtractCloudEventProperty(event, contribpubsub.TypeField),
		SpecVersion:     ExtractCloudEventProperty(event, contribpubsub.SpecVersionField),
	}

	if data, ok := event[contribpubsub.DataField]; ok && data != nil {
		envelope.Data = nil

		if contenttype.IsStringContentType(envelope.GetDataContentType()) {
			switch v := data.(type) {
			case string:
				envelope.Data = []byte(v)
			case []byte:
				envelope.Data = v
			default:
				return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{}, errUnexpectedEnvelopeData //nolint:nosnakecase
			}
		} else if contenttype.IsJSONContentType(envelope.GetDataContentType()) || contenttype.IsCloudEventContentType(envelope.GetDataContentType()) {
			envelope.Data, _ = json.Marshal(data)
		}
	}
	extensions, extensionsErr := ExtractCloudEventExtensions(event)
	if extensionsErr != nil {
		return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{}, extensionsErr
	}
	envelope.Extensions = extensions
	return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{CloudEvent: envelope}, nil //nolint:nosnakecase
}
