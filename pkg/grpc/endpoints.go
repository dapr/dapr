// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"net/http"

	"github.com/dapr/dapr/pkg/config"
	v1 "github.com/dapr/dapr/pkg/messaging/v1"
	"google.golang.org/grpc"
)

var endpoints = map[string][]string{
	"invoke.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/InvokeService",
	},
	"state.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/GetState",
		"/dapr.proto.runtime.v1.Dapr/GetBulkState",
		"/dapr.proto.runtime.v1.Dapr/SaveState",
		"/dapr.proto.runtime.v1.Dapr/DeleteState",
		"/dapr.proto.runtime.v1.Dapr/DeleteBulkState",
		"/dapr.proto.runtime.v1.Dapr/ExecuteStateTransaction",
	},
	"publish.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/PublishEvent",
	},
	"bindings.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/InvokeBinding",
	},
	"secrets.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/GetSecret",
		"/dapr.proto.runtime.v1.Dapr/GetBulkSecret",
	},
	"actors.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/RegisterActorTimer",
		"/dapr.proto.runtime.v1.Dapr/UnregisterActorTimer",
		"/dapr.proto.runtime.v1.Dapr/RegisterActorReminder",
		"/dapr.proto.runtime.v1.Dapr/UnregisterActorReminder",
		"/dapr.proto.runtime.v1.Dapr/GetActorState",
		"/dapr.proto.runtime.v1.Dapr/ExecuteActorStateTransaction",
		"/dapr.proto.runtime.v1.Dapr/InvokeActor",
	},
	"metadata.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/GetMetadata",
		"/dapr.proto.runtime.v1.Dapr/SetMetadata",
	},
	"shutdown.v1": []string{
		"/dapr.proto.runtime.v1.Dapr/Shutdown",
	},
}

func setAPIEndpointsMiddlewareUnary(rules []config.APIAccessRule) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		for _, rule := range rules {
			if list, ok := endpoints[rule.Name+"."+rule.Version]; ok {
				for _, method := range list {
					if method == info.FullMethod {
						err := v1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "requested endpoint is not authorized")
						return nil, err
					}
				}
			}
		}

		return handler(ctx, req)
	}
}
