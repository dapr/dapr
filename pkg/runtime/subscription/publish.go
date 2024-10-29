/*
Copyright 2024 The Dapr Authors
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

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

func (s *Subscription) publishMessageHTTP(ctx context.Context, msg *rtpubsub.SubscribedMessage) error {
	cloudEvent := msg.CloudEvent

	var span trace.Span

	req := invokev1.NewInvokeMethodRequest(msg.Path).
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(msg.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(msg.Metadata)
	defer req.Close()

	iTraceID := cloudEvent[contribpubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[contribpubsub.TraceIDField]
	}
	if iTraceID != nil {
		traceID := iTraceID.(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.Topic, sc, s.tracingSpec)
	}

	start := time.Now()
	resp, err := s.channels.AppChannel().InvokeMethod(ctx, req, "")
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(err))
	}
	defer resp.Close()

	statusCode := int(resp.Status().GetCode())

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse contribpubsub.AppResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appResponse)
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Debugf("skipping status check due to empty response body from pub/sub event %v", cloudEvent[contribpubsub.IDField])
			} else {
				log.Debugf("skipping status check due to error parsing result from pub/sub event %v: %s", cloudEvent[contribpubsub.IDField], err)
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
			return nil
		}

		switch appResponse.Status {
		case "":
			// Consider empty status field as success
			fallthrough
		case contribpubsub.Success:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
			return nil
		case contribpubsub.Retry:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
			// TODO: add retry error info
			return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
		case contribpubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msg.Topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
			return rtpubsub.ErrMessageDropped
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], appResponse.Status, rterrors.NewRetriable(nil))
	}

	body, _ := resp.RawDataFull()
	if statusCode == http.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], body, statusCode)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)
		return nil
	}

	// Every error from now on is a retriable error.
	errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.TopicField], body, statusCode)
	log.Warnf(errMsg)
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
	// return error status code for resiliency to decide on retry
	// TODO: Update types to uint32
	//nolint:gosec
	return resiliency.NewCodeError(int32(statusCode), rterrors.NewRetriable(errors.New(errMsg)))
}

func (s *Subscription) publishMessageGRPC(ctx context.Context, msg *rtpubsub.SubscribedMessage) error {
	cloudEvent := msg.CloudEvent

	envelope, span, err := rtpubsub.GRPCEnvelopeFromSubscriptionMessage(ctx, msg, log, s.tracingSpec)
	if err != nil {
		return err
	}

	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.Metadata)

	conn, err := s.grpc.GetAppClient()
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
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)

			return nil
		}

		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(err))
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)

		// return error status code for resiliency to decide on retry
		// TODO: Update types to uint32
		//nolint:gosec
		if hasErrStatus {
			return resiliency.NewCodeError(int32(errStatus.Code()), err)
		}

		// on error from application, return error for redelivery of event
		return err
	}

	switch res.GetStatus() {
	case runtimev1.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
		return nil
	case runtimev1.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		// TODO: add retry error info
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
	case runtimev1.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msg.Topic, elapsed)

		return rtpubsub.ErrMessageDropped
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
	return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], res.GetStatus(), rterrors.NewRetriable(nil))
}
