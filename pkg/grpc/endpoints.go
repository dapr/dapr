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
	v1 "github.com/dapr/dapr/pkg/messaging/v1"
)

var endpoints = map[string][]string{
	"invoke.v1": {
		"/dapr.proto.runtime.v1.Dapr/InvokeService",
	},
	"state.v1": {
		"/dapr.proto.runtime.v1.Dapr/GetState",
		"/dapr.proto.runtime.v1.Dapr/GetBulkState",
		"/dapr.proto.runtime.v1.Dapr/SaveState",
		"/dapr.proto.runtime.v1.Dapr/QueryState",
		"/dapr.proto.runtime.v1.Dapr/DeleteState",
		"/dapr.proto.runtime.v1.Dapr/DeleteBulkState",
		"/dapr.proto.runtime.v1.Dapr/ExecuteStateTransaction",
	},
	"publish.v1": {
		"/dapr.proto.runtime.v1.Dapr/PublishEvent",
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
	"shutdown.v1": {
		"/dapr.proto.runtime.v1.Dapr/Shutdown",
	},
}

const protocol = "grpc"

func setAPIEndpointsMiddlewareUnary(rules []config.APIAccessRule) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		var grpcRules []config.APIAccessRule

		for _, rule := range rules {
			if rule.Protocol == protocol {
				grpcRules = append(grpcRules, rule)
			}
		}

		if len(grpcRules) == 0 {
			return handler(ctx, req)
		}

		for _, rule := range grpcRules {
			if list, ok := endpoints[rule.Name+"."+rule.Version]; ok {
				for _, method := range list {
					if method == info.FullMethod {
						return handler(ctx, req)
					}
				}
			}
		}

		err := v1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
		return nil, err
	}
}
