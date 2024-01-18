/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (p *pubsub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	ps, ok := p.compStore.GetPubSub(req.PubsubName)
	if !ok {
		return rtpubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := p.isOperationAllowed(req.PubsubName, req.Topic, ps.ScopedPublishings); !allowed {
		return rtpubsub.NotAllowedError{Topic: req.Topic, ID: p.id}
	}

	if ps.NamespaceScoped {
		req.Topic = p.namespace + req.Topic
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		p.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, ps.Component.Publish(ctx, req)
	})
	return err
}

func (p *pubsub) BulkPublish(ctx context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	ps, ok := p.compStore.GetPubSub(req.PubsubName)
	if !ok {
		return contribpubsub.BulkPublishResponse{}, rtpubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := p.isOperationAllowed(req.PubsubName, req.Topic, ps.ScopedPublishings); !allowed {
		return contribpubsub.BulkPublishResponse{}, rtpubsub.NotAllowedError{Topic: req.Topic, ID: p.id}
	}

	policyDef := p.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub)

	if contribpubsub.FeatureBulkPublish.IsPresent(ps.Component.Features()) {
		return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, ps.Component.(contribpubsub.BulkPublisher))
	}

	log.Debugf("pubsub %s does not implement the BulkPublish API; falling back to publishing messages individually", req.PubsubName)
	defaultBulkPublisher := rtpubsub.NewDefaultBulkPublisher(ps.Component)

	return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, defaultBulkPublisher)
}

func (p *pubsub) publishMessageHTTP(ctx context.Context, msg *subscribedMessage) error {
	cloudEvent := msg.cloudEvent

	var span trace.Span

	req := invokev1.NewInvokeMethodRequest(msg.path).
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(msg.data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(msg.metadata)
	defer req.Close()

	iTraceID := cloudEvent[contribpubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[contribpubsub.TraceIDField]
	}
	if iTraceID != nil {
		traceID := iTraceID.(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.topic, sc, p.tracingSpec)
	}

	start := time.Now()
	resp, err := p.channels.AppChannel().InvokeMethod(ctx, req, "")
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(err))
	}
	defer resp.Close()

	statusCode := int(resp.Status().GetCode())

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse contribpubsub.AppResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appResponse)
		if err != nil {
			log.Debugf("skipping status check due to error parsing result from pub/sub event %v: %s", cloudEvent[contribpubsub.IDField], err)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Success)), msg.topic, elapsed)
			return nil
		}

		switch appResponse.Status {
		case "":
			// Consider empty status field as success
			fallthrough
		case contribpubsub.Success:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Success)), msg.topic, elapsed)
			return nil
		case contribpubsub.Retry:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
			// TODO: add retry error info
			return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
		case contribpubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Drop)), msg.topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
			return rtpubsub.ErrMessageDropped
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], appResponse.Status, rterrors.NewRetriable(nil))
	}

	body, _ := resp.RawDataFull()
	if statusCode == http.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], body, statusCode)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Drop)), msg.topic, elapsed)
		return nil
	}

	// Every error from now on is a retriable error.
	errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.TopicField], body, statusCode)
	log.Warnf(errMsg)
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
	return rterrors.NewRetriable(errors.New(errMsg))
}

func (p *pubsub) publishMessageGRPC(ctx context.Context, msg *subscribedMessage) error {
	cloudEvent := msg.cloudEvent

	envelope := &runtimev1.TopicEventRequest{
		Id:              ExtractCloudEventProperty(cloudEvent, contribpubsub.IDField),
		Source:          ExtractCloudEventProperty(cloudEvent, contribpubsub.SourceField),
		DataContentType: ExtractCloudEventProperty(cloudEvent, contribpubsub.DataContentTypeField),
		Type:            ExtractCloudEventProperty(cloudEvent, contribpubsub.TypeField),
		SpecVersion:     ExtractCloudEventProperty(cloudEvent, contribpubsub.SpecVersionField),
		Topic:           msg.topic,
		PubsubName:      msg.metadata[metadataKeyPubSub],
		Path:            msg.path,
	}

	if data, ok := cloudEvent[contribpubsub.DataBase64Field]; ok && data != nil {
		if dataAsString, ok := data.(string); ok {
			decoded, decodeErr := base64.StdEncoding.DecodeString(dataAsString)
			if decodeErr != nil {
				log.Debugf("unable to base64 decode cloudEvent field data_base64: %s", decodeErr)
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, 0)

				return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(decodeErr))
			}

			envelope.Data = decoded
		} else {
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, 0)
			return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(errUnexpectedEnvelopeData))
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
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, 0)
				return fmt.Errorf("error returned from app while processing pub/sub event: %w", rterrors.NewRetriable(errUnexpectedEnvelopeData))
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
			spanName := fmt.Sprintf("pubsub/%s", msg.topic)

			// no ops if trace is off
			ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, p.tracingSpec)
			// span is nil if tracing is disabled (sampling rate is 0)
			if span != nil {
				ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
			}
		} else {
			log.Warnf("ignored non-string traceid value: %v", iTraceID)
		}
	}

	extensions, extensionsErr := extractCloudEventExtensions(cloudEvent)
	if extensionsErr != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, 0)
		return extensionsErr
	}
	envelope.Extensions = extensions

	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.metadata)

	conn, err := p.grpc.GetAppClient()
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := runtimev1.NewAppCallbackClient(conn)

	start := time.Now()
	res, err := clientV1.OnTopicEvent(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.GetTopic())
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
		span.End()
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing pub/sub event %v: %s", cloudEvent[contribpubsub.IDField], err)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Drop)), msg.topic, elapsed)

			return nil
		}

		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(err))
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)

		// on error from application, return error for redelivery of event
		return err
	}

	switch res.GetStatus() {
	case runtimev1.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Success)), msg.topic, elapsed)
		return nil
	case runtimev1.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
		// TODO: add retry error info
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
	case runtimev1.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Drop)), msg.topic, elapsed)

		return rtpubsub.ErrMessageDropped
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(contribpubsub.Retry)), msg.topic, elapsed)
	return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], res.GetStatus(), rterrors.NewRetriable(nil))
}

func extractCloudEventExtensions(cloudEvent map[string]interface{}) (*structpb.Struct, error) {
	// Assemble Cloud Event Extensions:
	// Create copy of the cloud event with duplicated data removed

	extensions := map[string]interface{}{}
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
