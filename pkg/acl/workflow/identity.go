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

package workflow

import (
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Callers MUST authenticate the identity before stamping (mTLS/SPIFFE for
// remote calls, local sidecar trust for local calls); this helper does not.
func SetCallerIdentity(req *internalv1pb.InternalInvokeRequest, appID, namespace string) {
	if req == nil {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]*internalv1pb.ListStringValue, 2)
	}
	if appID != "" {
		req.Metadata[invokev1.CallerIDHeader] = &internalv1pb.ListStringValue{Values: []string{appID}}
	}
	if namespace != "" {
		req.Metadata[invokev1.CallerNamespaceHeader] = &internalv1pb.ListStringValue{Values: []string{namespace}}
	}
}

func CallerAppID(md map[string]*internalv1pb.ListStringValue) string {
	v, ok := md[invokev1.CallerIDHeader]
	if !ok || len(v.GetValues()) == 0 {
		return ""
	}
	return v.GetValues()[0]
}

func CallerNamespace(md map[string]*internalv1pb.ListStringValue) string {
	v, ok := md[invokev1.CallerNamespaceHeader]
	if !ok || len(v.GetValues()) == 0 {
		return ""
	}
	return v.GetValues()[0]
}
