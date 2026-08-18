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
	"strings"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// IsInternalActorType reports whether actorType is a Dapr-reserved internal
// actor type (workflow, activity, executor, retentioner, ...). User-facing
// actor APIs (state, reminder, timer) must reject these because the workflow
// runtime owns their lifecycle. Direct access from a user would corrupt state
// or bypass per-operation policy enforcement.
func IsInternalActorType(actorType string) bool {
	return strings.HasPrefix(actorType, actorTypePrefix)
}

// StripUntrustedCallerIdentity removes the caller-identity headers from
// metadata that arrived from an untrusted source (a user-facing API like
// InvokeActor or onDirectActorMessage that copies client metadata
// verbatim). Without this, a local app could spoof another app's identity
// by setting the caller-app-id / caller-namespace headers in their request.
// Trusted code paths (the CallActor gRPC handler stamping SPIFFE identity,
// the router stamping the local sidecar's identity) re-set these headers
// after stripping.
func StripUntrustedCallerIdentity(md map[string]*internalv1pb.ListStringValue) {
	if md == nil {
		return
	}
	delete(md, invokev1.CallerIDHeader)
	delete(md, invokev1.CallerNamespaceHeader)
}

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
