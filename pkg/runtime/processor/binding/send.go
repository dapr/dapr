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

package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	md "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/state"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (b *binding) StartReadingFromBindings(ctx context.Context) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.stopForever {
		return nil
	}

	b.readingBindings = true

	if b.channels.AppChannel() == nil {
		return errors.New("app channel not initialized")
	}

	// Clean any previous state
	for _, cancel := range b.inputCancels {
		cancel()
	}
	b.inputCancels = make(map[string]context.CancelFunc)

	comps := b.compStore.ListComponents()
	bindings := make(map[string]componentsV1alpha1.Component)
	for i, c := range comps {
		if strings.HasPrefix(c.Spec.Type, string(components.CategoryBindings)) {
			bindings[c.ObjectMeta.Name] = comps[i]
		}
	}

	for name, bind := range b.compStore.ListInputBindings() {
		if err := b.startInputBinding(bindings[name], bind); err != nil {
			return err
		}
	}

	return nil
}

func (b *binding) startInputBinding(comp componentsV1alpha1.Component, binding bindings.InputBinding) error {
	var isSubscribed bool

	meta, err := b.meta.ToBaseMetadata(comp)
	if err != nil {
		return err
	}

	m := meta.Properties

	ctx, cancel := context.WithCancel(context.Background())
	if isBindingOfExplicitDirection(ComponentTypeInput, m) {
		isSubscribed = true
	} else {
		var err error
		isSubscribed, err = b.isAppSubscribedToBinding(ctx, comp.Name)
		if err != nil {
			cancel()
			return err
		}
	}

	if !isSubscribed {
		log.Infof("app has not subscribed to binding %s.", comp.Name)
		cancel()
		return nil
	}

	if err := b.readFromBinding(ctx, comp.Name, binding); err != nil {
		log.Errorf("error reading from input binding %s: %s", comp.Name, err)
		cancel()
		return nil
	}

	b.inputCancels[comp.Name] = cancel
	return nil
}

func (b *binding) StopReadingFromBindings(forever bool) {
	b.lock.Lock()
	defer b.lock.Unlock()
	defer b.wg.Wait()

	if forever {
		b.stopForever = true
	}

	b.readingBindings = false

	for _, cancel := range b.inputCancels {
		cancel()
	}
	b.inputCancels = make(map[string]context.CancelFunc)
}

func (b *binding) sendBatchOutputBindingsParallel(ctx context.Context, to []string, data []byte) {
	b.wg.Add(len(to))
	for _, dst := range to {
		go func(name string) {
			defer b.wg.Done()

			_, err := b.SendToOutputBinding(ctx, name, &bindings.InvokeRequest{
				Data:      data,
				Operation: bindings.CreateOperation,
			})
			if err != nil {
				log.Error(err)
			}
		}(dst)
	}
}

func (b *binding) sendBatchOutputBindingsSequential(ctx context.Context, to []string, data []byte) error {
	for _, dst := range to {
		_, err := b.SendToOutputBinding(ctx, dst, &bindings.InvokeRequest{
			Data:      data,
			Operation: bindings.CreateOperation,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *binding) SendToOutputBinding(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	if req.Operation == "" {
		return nil, errors.New("operation field is missing from request")
	}

	if binding, ok := b.compStore.GetOutputBinding(name); ok {
		ops := binding.Operations()
		for _, o := range ops {
			if o == req.Operation {
				policyRunner := resiliency.NewRunner[*bindings.InvokeResponse](ctx,
					b.resiliency.ComponentOutboundPolicy(name, resiliency.Binding),
				)
				return policyRunner(func(ctx context.Context) (*bindings.InvokeResponse, error) {
					return binding.Invoke(ctx, req)
				})
			}
		}
		supported := make([]string, 0, len(ops))
		for _, o := range ops {
			supported = append(supported, string(o))
		}
		return nil, fmt.Errorf("binding %s does not support operation %s. supported operations:%s", name, req.Operation, strings.Join(supported, " "))
	}
	return nil, fmt.Errorf("couldn't find output binding %s", name)
}

func (b *binding) onAppResponse(ctx context.Context, response *bindings.AppResponse) error {
	if len(response.State) > 0 {
		b.wg.Add(1)
		go func(reqs []state.SetRequest) {
			defer b.wg.Done()

			store, ok := b.compStore.GetStateStore(response.StoreName)
			if !ok {
				return
			}

			err := stateLoader.PerformBulkStoreOperation(ctx, reqs,
				b.resiliency.ComponentOutboundPolicy(response.StoreName, resiliency.Statestore),
				state.BulkStoreOpts{},
				store.Set,
				store.BulkSet,
			)
			if err != nil {
				log.Errorf("error saving state from app response: %v", err)
			}
		}(response.State)
	}

	if len(response.To) > 0 {
		data, err := json.Marshal(&response.Data)
		if err != nil {
			return err
		}

		if response.Concurrency == ConcurrencyParallel {
			b.sendBatchOutputBindingsParallel(ctx, response.To, data)
		} else {
			return b.sendBatchOutputBindingsSequential(ctx, response.To, data)
		}
	}

	return nil
}

func (b *binding) sendBindingEventToApp(ctx context.Context, bindingName string, data []byte, metadata map[string]string) ([]byte, error) {
	var response bindings.AppResponse
	spanName := "bindings/" + bindingName
	spanContext := trace.SpanContext{}

	// Check the grpc-trace-bin with fallback to traceparent.
	validTraceparent := false
	if val, ok := metadata[diag.GRPCTraceContextKey]; ok {
		if sc, ok := diagUtils.SpanContextFromBinary([]byte(val)); ok {
			spanContext = sc
		}
	} else if val, ok := metadata[diag.TraceparentHeader]; ok {
		if sc, ok := diag.SpanContextFromW3CString(val); ok {
			spanContext = sc
			validTraceparent = true
			// Only parse the tracestate if we've successfully parsed the traceparent.
			if val, ok := metadata[diag.TracestateHeader]; ok {
				ts := diag.TraceStateFromW3CString(val)
				spanContext.WithTraceState(*ts)
			}
		}
	}
	// span is nil if tracing is disabled (sampling rate is 0)
	ctx, span := diag.StartInternalCallbackSpan(ctx, spanName, spanContext, b.tracingSpec)

	var appResponseBody []byte
	path, _ := b.compStore.GetInputBindingRoute(bindingName)
	if path == "" {
		path = bindingName
	}

	if !b.isHTTP {
		if span != nil {
			ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
		}

		// Add workaround to fallback on checking traceparent header.
		// As grpc-trace-bin is not yet there in OpenTelemetry unlike OpenCensus, tracking issue https://github.com/open-telemetry/opentelemetry-specification/issues/639
		// and grpc-dotnet client adheres to OpenTelemetry Spec which only supports http based traceparent header in gRPC path.
		// TODO: Remove this workaround fix once grpc-dotnet supports grpc-trace-bin header. Tracking issue https://github.com/dapr/dapr/issues/1827.
		if validTraceparent && span != nil {
			spanContextHeaders := make(map[string]string, 2)
			diag.SpanContextToHTTPHeaders(span.SpanContext(), func(key string, val string) {
				spanContextHeaders[key] = val
			})
			for key, val := range spanContextHeaders {
				ctx = md.AppendToOutgoingContext(ctx, key, val)
			}
		}

		conn, err := b.grpc.GetAppClient()
		if err != nil {
			return nil, fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		req := &runtimev1pb.BindingEventRequest{
			Name:     bindingName,
			Data:     data,
			Metadata: metadata,
		}
		start := time.Now()

		policyRunner := resiliency.NewRunner[*runtimev1pb.BindingEventResponse](ctx,
			b.resiliency.ComponentInboundPolicy(bindingName, resiliency.Binding),
		)
		resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.BindingEventResponse, error) {
			return client.OnBindingEvent(ctx, req)
		})

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				"/dapr.proto.runtime.v1.AppCallback/OnBindingEvent")
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromGRPCError(span, err)
			span.End()
		}
		if diag.DefaultGRPCMonitoring.IsEnabled() {
			diag.DefaultGRPCMonitoring.ServerRequestSent(ctx,
				"/dapr.proto.runtime.v1.AppCallback/OnBindingEvent",
				status.Code(err).String(),
				int64(len(req.GetData())), int64(len(resp.GetData())),
				start)
		}

		if err != nil {
			return nil, fmt.Errorf("error invoking app: %w", err)
		}
		if resp != nil {
			if resp.GetConcurrency() == runtimev1pb.BindingEventResponse_PARALLEL { //nolint:nosnakecase
				response.Concurrency = ConcurrencyParallel
			} else {
				response.Concurrency = ConcurrencySequential
			}

			response.To = resp.GetTo()

			if resp.GetData() != nil {
				appResponseBody = resp.GetData()

				var d interface{}
				err := json.Unmarshal(resp.GetData(), &d)
				if err == nil {
					response.Data = d
				}
			}
		}
	} else {
		policyDef := b.resiliency.ComponentInboundPolicy(bindingName, resiliency.Binding)

		reqMetadata := make(map[string][]string, len(metadata))
		for k, v := range metadata {
			reqMetadata[k] = []string{v}
		}
		req := invokev1.NewInvokeMethodRequest(path).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(data).
			WithContentType(invokev1.JSONContentType).
			WithMetadata(reqMetadata)
		if policyDef != nil {
			req.WithReplay(policyDef.HasRetries())
		}
		defer req.Close()

		respErr := errors.New("error sending binding event to application")
		policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
			resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
				Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
			},
		)
		resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
			rResp, rErr := b.channels.AppChannel().InvokeMethod(ctx, req, "")
			if rErr != nil {
				return rResp, rErr
			}
			if rResp != nil && rResp.Status().GetCode() != http.StatusOK {
				return rResp, fmt.Errorf("%w, status %d", respErr, rResp.Status().GetCode())
			}
			return rResp, nil
		})
		if err != nil && !errors.Is(err, respErr) {
			return nil, fmt.Errorf("error invoking app: %w", err)
		}

		if resp == nil {
			return nil, errors.New("error invoking app: response object is nil")
		}
		defer resp.Close()

		if span != nil {
			m := diag.ConstructInputBindingSpanAttributes(
				bindingName,
				http.MethodPost+" /"+bindingName,
			)
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, int(resp.Status().GetCode()))
			span.End()
		}

		appResponseBody, err = resp.RawDataFull()

		// ::TODO report metrics for http, such as grpc
		if code := resp.Status().GetCode(); code < 200 || code > 299 {
			return nil, fmt.Errorf("fails to send binding event to http app channel, status code: %d body: %s", code, string(appResponseBody))
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}

	if len(response.State) > 0 || len(response.To) > 0 {
		if err := b.onAppResponse(ctx, &response); err != nil {
			log.Errorf("error executing app response: %s", err)
		}
	}

	return appResponseBody, nil
}

func (b *binding) readFromBinding(readCtx context.Context, name string, binding bindings.InputBinding) error {
	return binding.Read(readCtx, func(ctx context.Context, resp *bindings.ReadResponse) ([]byte, error) {
		if resp == nil {
			return nil, nil
		}

		start := time.Now()
		b, err := b.sendBindingEventToApp(ctx, name, resp.Data, resp.Metadata)
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.InputBindingEvent(context.Background(), name, err == nil, elapsed)

		if err != nil {
			log.Debugf("error from app consumer for binding [%s]: %s", name, err)
			return nil, err
		}
		return b, nil
	})
}

func (b *binding) getSubscribedBindingsGRPC(ctx context.Context) ([]string, error) {
	conn, err := b.grpc.GetAppClient()
	if err != nil {
		return nil, fmt.Errorf("error while getting app client: %w", err)
	}
	client := runtimev1pb.NewAppCallbackClient(conn)
	resp, err := client.ListInputBindings(ctx, &emptypb.Empty{})
	bindings := []string{}

	if err == nil && resp != nil {
		bindings = resp.GetBindings()
	}
	return bindings, nil
}

func (b *binding) isAppSubscribedToBinding(ctx context.Context, binding string) (bool, error) {
	// if gRPC, looks for the binding in the list of bindings returned from the app
	if !b.isHTTP {
		if b.subscribeBindingList == nil {
			list, err := b.getSubscribedBindingsGRPC(ctx)
			if err != nil {
				return false, err
			}
			b.subscribeBindingList = list
		}
		for _, b := range b.subscribeBindingList {
			if b == binding {
				return true, nil
			}
		}
	} else {
		// if HTTP, check if there's an endpoint listening for that binding
		path, _ := b.compStore.GetInputBindingRoute(binding)
		req := invokev1.NewInvokeMethodRequest(path).
			WithHTTPExtension(http.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer req.Close()

		resp, err := b.channels.AppChannel().InvokeMethod(ctx, req, "")
		if err != nil {
			return false, fmt.Errorf("could not invoke OPTIONS method on input binding subscription endpoint %q: %v", path, err)
		}
		defer resp.Close()
		code := resp.Status().GetCode()

		return code/100 == 2 || code == http.StatusMethodNotAllowed, nil
	}
	return false, nil
}

func isBindingOfExplicitDirection(direction string, metadata map[string]string) bool {
	for k, v := range metadata {
		if strings.EqualFold(k, ComponentDirection) {
			directions := strings.Split(v, ",")
			for _, d := range directions {
				if strings.TrimSpace(strings.ToLower(d)) == direction {
					return true
				}
			}
		}
	}

	return false
}
