/*
Copyright 2026 The Dapr Authors
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
	"google.golang.org/grpc/codes"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/pubsub/transport"
)

// TransportMode aliases transport.Mode so callers can keep using
// rtpubsub.TransportMode. The concrete type lives in the leaf transport package
// to avoid an import cycle with the Adapter fakes/mocks.
type TransportMode = transport.Mode

const (
	// TransportModeGRPC matches broker errors against `gRPCStatusCodes`, using the
	// component's native gRPC status code. Passed by the gRPC publish API and by
	// internal republish paths (dead-letter, outbox).
	TransportModeGRPC TransportMode = iota
	// TransportModeHTTP matches broker errors against `httpStatusCodes`, mapping the
	// component's gRPC status code to its HTTP equivalent. Passed by the HTTP
	// publish API.
	TransportModeHTTP
)

// NewPublishResiliencyError wraps a pub/sub component (bulk) publish error in a
// resiliency.CodeError so retry `matching` can evaluate it. For HTTP publishes
// the gRPC status code is mapped to its HTTP equivalent (matched against
// `httpStatusCodes`); otherwise the gRPC code is used (matched against
// `gRPCStatusCodes`).
func NewPublishResiliencyError(mode TransportMode, code codes.Code, err error) resiliency.CodeError {
	//nolint:gosec // gRPC status codes (0-16) fit in int32.
	statusCode := int32(code)
	if mode == TransportModeHTTP {
		//nolint:gosec // HTTP status codes (100-599) fit in int32.
		statusCode = int32(invokev1.HTTPStatusFromCode(code))
	}
	return resiliency.NewCodeError(statusCode, err)
}
