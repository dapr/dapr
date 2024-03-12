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

package grpc

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

const daprRuntimePrefix = "/dapr.proto.runtime."

var endpoints = map[string][]string{
	"invoke.v1": {
		daprRuntimePrefix + "v1.Dapr/InvokeService",
	},
	"state.v1": {
		daprRuntimePrefix + "v1.Dapr/GetState",
		daprRuntimePrefix + "v1.Dapr/GetBulkState",
		daprRuntimePrefix + "v1.Dapr/SaveState",
		daprRuntimePrefix + "v1.Dapr/DeleteState",
		daprRuntimePrefix + "v1.Dapr/DeleteBulkState",
		daprRuntimePrefix + "v1.Dapr/ExecuteStateTransaction",
	},
	"state.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/QueryStateAlpha1",
	},
	"publish.v1": {
		daprRuntimePrefix + "v1.Dapr/PublishEvent",
	},
	"publish.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/BulkPublishEventAlpha1",
	},
	"bindings.v1": {
		daprRuntimePrefix + "v1.Dapr/InvokeBinding",
	},
	"secrets.v1": {
		daprRuntimePrefix + "v1.Dapr/GetSecret",
		daprRuntimePrefix + "v1.Dapr/GetBulkSecret",
	},
	"actors.v1": {
		daprRuntimePrefix + "v1.Dapr/RegisterActorTimer",
		daprRuntimePrefix + "v1.Dapr/UnregisterActorTimer",
		daprRuntimePrefix + "v1.Dapr/RegisterActorReminder",
		daprRuntimePrefix + "v1.Dapr/UnregisterActorReminder",
		daprRuntimePrefix + "v1.Dapr/GetActorState",
		daprRuntimePrefix + "v1.Dapr/ExecuteActorStateTransaction",
		daprRuntimePrefix + "v1.Dapr/InvokeActor",
	},
	"metadata.v1": {
		daprRuntimePrefix + "v1.Dapr/GetMetadata",
		daprRuntimePrefix + "v1.Dapr/SetMetadata",
	},
	"configuration.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/GetConfigurationAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubscribeConfigurationAlpha1",
		daprRuntimePrefix + "v1.Dapr/UnsubscribeConfigurationAlpha1",
	},
	"configuration.v1": {
		daprRuntimePrefix + "v1.Dapr/GetConfiguration",
		daprRuntimePrefix + "v1.Dapr/SubscribeConfiguration",
		daprRuntimePrefix + "v1.Dapr/UnsubscribeConfiguration",
	},
	"lock.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/TryLockAlpha1",
	},
	"unlock.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/UnlockAlpha1",
	},
	"subtlecrypto.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/SubtleGetKeyAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleEncryptAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleDecryptAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleSignAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleVerifyAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleWrapKeyAlpha1",
		daprRuntimePrefix + "v1.Dapr/SubtleUnwrapKeyAlpha1",
	},
	"crypto.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/EncryptAlpha1",
		daprRuntimePrefix + "v1.Dapr/DecryptAlpha1",
	},
	"workflows.v1alpha1": {
		daprRuntimePrefix + "v1.Dapr/StartWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/GetWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/TerminateWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/RaiseEventWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/PurgeWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/PauseWorkflowAlpha1",
		daprRuntimePrefix + "v1.Dapr/ResumeWorkflowAlpha1",
	},
	"workflows.v1beta1": {
		daprRuntimePrefix + "v1.Dapr/StartWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/GetWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/TerminateWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/RaiseEventWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/PurgeWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/PauseWorkflowBeta1",
		daprRuntimePrefix + "v1.Dapr/ResumeWorkflowBeta1",
	},
	"shutdown.v1": {
		daprRuntimePrefix + "v1.Dapr/Shutdown",
	},
}

// Returns the middlewares (unary and stream) for supporting API allowlist
func setAPIEndpointsMiddlewares(allowedRules config.APIAccessRules, deniedRules config.APIAccessRules) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	allowed := apiAccessRuleToMap(allowedRules)
	denied := apiAccessRuleToMap(deniedRules)

	// Passthrough if no gRPC rules
	if len(allowed) == 0 && len(denied) == 0 {
		return nil, nil
	}

	// Return the unary middleware function
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			// Apply the allowlist only on methods that are part of the Dapr runtime, or it will interfere with gRPC proxying
			if strings.HasPrefix(info.FullMethod, daprRuntimePrefix) {
				if len(denied) > 0 {
					_, ok := denied[info.FullMethod]
					if ok {
						return nil, invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
					}
				}
				if len(allowed) > 0 {
					_, ok := allowed[info.FullMethod]
					if !ok {
						return nil, invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
					}
				}
			}

			return handler(ctx, req)
		},
		func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			// Apply the allowlist only on methods that are part of the Dapr runtime, or it will interfere with gRPC proxying
			if strings.HasPrefix(info.FullMethod, daprRuntimePrefix) {
				if len(denied) > 0 {
					_, ok := denied[info.FullMethod]
					if ok {
						return invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
					}
				}
				if len(allowed) > 0 {
					_, ok := allowed[info.FullMethod]
					if !ok {
						return invokev1.ErrorFromHTTPResponseCode(http.StatusNotImplemented, "requested endpoint is not available")
					}
				}
			}

			return handler(srv, stream)
		}
}

// Converts a slice of config.APIAccessRule into a map where the key is the gRPC full endpoint.
// The keys in the returned map follow the pattern "<rule-name>.<rule-version>"
func apiAccessRuleToMap(rules config.APIAccessRules) map[string]struct{} {
	res := map[string]struct{}{}

	for _, rule := range rules {
		if rule.Protocol != "grpc" {
			continue
		}

		if list, ok := endpoints[rule.Name+"."+rule.Version]; ok {
			for _, method := range list {
				res[method] = struct{}{}
			}
		}
	}

	return res
}
