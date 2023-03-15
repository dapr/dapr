/*
Copyright 2021 The Dapr Authors
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

package grpc

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

var endpoints = map[string][]string{
	"invoke.v1": {
		"/dapr.proto.runtime.v1.Dapr/InvokeService",
	},
	"state.v1": {
		"/dapr.proto.runtime.v1.Dapr/GetState",
		"/dapr.proto.runtime.v1.Dapr/GetBulkState",
		"/dapr.proto.runtime.v1.Dapr/SaveState",
		"/dapr.proto.runtime.v1.Dapr/DeleteState",
		"/dapr.proto.runtime.v1.Dapr/DeleteBulkState",
		"/dapr.proto.runtime.v1.Dapr/ExecuteStateTransaction",
	},
	"state.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/QueryStateAlpha1",
	},
	"publish.v1": {
		"/dapr.proto.runtime.v1.Dapr/PublishEvent",
	},
	"publish.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/BulkPublishEventAlpha1",
	},
	"bindings.v1": {
		"/dapr.proto.runtime.v1.Dapr/InvokeBinding",
	},
	"secrets.v1": {
		"/dapr.proto.runtime.v1.Dapr/GetSecret",
		"/dapr.proto.runtime.v1.Dapr/GetBulkSecret",
	},
	"actors.v1": {
		"/dapr.proto.runtime.v1.Dapr/RegisterActorTimer",
		"/dapr.proto.runtime.v1.Dapr/UnregisterActorTimer",
		"/dapr.proto.runtime.v1.Dapr/RegisterActorReminder",
		"/dapr.proto.runtime.v1.Dapr/UnregisterActorReminder",
		"/dapr.proto.runtime.v1.Dapr/RenameActorReminder",
		"/dapr.proto.runtime.v1.Dapr/GetActorState",
		"/dapr.proto.runtime.v1.Dapr/ExecuteActorStateTransaction",
		"/dapr.proto.runtime.v1.Dapr/InvokeActor",
	},
	"metadata.v1": {
		"/dapr.proto.runtime.v1.Dapr/GetMetadata",
		"/dapr.proto.runtime.v1.Dapr/SetMetadata",
	},
	"configuration.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/GetConfigurationAlpha1",
		"/dapr.proto.runtime.v1.Dapr/SubscribeConfigurationAlpha1",
		"/dapr.proto.runtime.v1.Dapr/UnsubscribeConfigurationAlpha1",
	},
	"lock.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/TryLockAlpha1",
	},
	"unlock.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/UnlockAlpha1",
	},
	"workflows.v1alpha1": {
		"/dapr.proto.runtime.v1.Dapr/StartWorkflowAlpha1",
		"/dapr.proto.runtime.v1.Dapr/GetWorkflowAlpha1",
		"/dapr.proto.runtime.v1.Dapr/TerminateWorkflowAlpha1",
	},
	"shutdown.v1": {
		"/dapr.proto.runtime.v1.Dapr/Shutdown",
	},
}

// Returns the middlewares (unary and stream) for supporting API allowlist
func setAPIEndpointsMiddlewares(rules []config.APIAccessRule) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	allowed := map[string]struct{}{}

	for _, rule := range rules {
		if rule.Protocol != "grpc" {
			continue
		}

		if list, ok := endpoints[rule.Name+"."+rule.Version]; ok {
			for _, method := range list {
				allowed[method] = struct{}{}
			}
		}
	}

	// Passthrough if no gRPC rules
	if len(allowed) == 0 {
		return nil, nil
	}

	// Return the unary middleware function
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			_, ok := allowed[info.FullMethod]
			if !ok {
				return nil, invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
			}

			return handler(ctx, req)
		},
		func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			_, ok := allowed[info.FullMethod]
			if !ok {
				return invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
			}
			return handler(srv, stream)
		}
}
