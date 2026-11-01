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

// Package hostauthz authorizes an actor host report against the caller's
// SPIFFE identity, shared by the placement service and the scheduler's
// placement server.
package hostauthz

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.actors.hostauthz")

// Report is the identity-relevant content of an actor host report.
type Report struct {
	AppID      string
	Namespace  string
	ActorTypes []string
}

const internalActorTypePrefix = "dapr.internal."

// Authorize rejects a report whose app ID or namespace does not match the
// caller's SPIFFE identity, and reports claiming internal actor types
// (dapr.internal.<namespace>.<appid>.*) which do not belong to the reporting
// app. With mTLS disabled only the actor type check applies.
func Authorize(ctx context.Context, sec security.Handler, r Report) error {
	if sec.MTLSEnabled() {
		clientID, ok, err := spiffe.FromGRPCContext(ctx)
		if err != nil || !ok {
			log.Debugf("failed to get client ID from context: err=%v, ok=%t", err, ok)
			return status.Errorf(codes.Unauthenticated, "failed to get client ID from context")
		}

		if r.AppID != clientID.AppID() {
			return status.Errorf(
				codes.PermissionDenied,
				"provided app ID %s doesn't match the one in the SPIFFE ID (%s)",
				r.AppID, clientID.AppID(),
			)
		}

		if r.Namespace != clientID.Namespace() {
			return status.Errorf(
				codes.PermissionDenied,
				"provided client namespace %s doesn't match the one in the SPIFFE ID (%s)",
				r.Namespace, clientID.Namespace(),
			)
		}
	}

	for _, actorType := range r.ActorTypes {
		if internal, owned := InternalActorType(actorType, r.Namespace, r.AppID); internal && !owned {
			return status.Errorf(
				codes.PermissionDenied,
				"entity %s is not allowed for app ID %s in namespace %s",
				actorType, r.AppID, r.Namespace,
			)
		}
	}

	return nil
}

// InternalActorType reports whether actorType is a reserved internal actor
// type (dapr.internal.*) and, if so, whether it belongs to appID in namespace
// ns, that is whether it is "dapr.internal.<ns>.<appID>" or starts with it
// followed by a dot. The owner is matched as a prefix rather than parsed out
// of the type because a self-hosted namespace may contain dots; an app ID
// cannot, so the match is unambiguous within a namespace. A malformed
// internal type, such as one with an empty segment, is internal and owned by
// no one.
func InternalActorType(actorType, ns, appID string) (internal, owned bool) {
	if actorType != "dapr.internal" && !strings.HasPrefix(actorType, internalActorTypePrefix) {
		return false, false
	}
	if ns == "" || appID == "" {
		return true, false
	}
	owner := internalActorTypePrefix + ns + "." + appID
	return true, actorType == owner || strings.HasPrefix(actorType, owner+".")
}
