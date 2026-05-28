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

package signing

import (
	"sync"

	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.orchestrator.signing")

// Signing carries the per-orchestrator signing state. SignNewEvents,
// AttachChildCompletionAttestation, and VerifyInboxAttestation are
// no-ops when Signer is nil.
type Signing struct {
	Signer            *signer.Signer
	Namespace         string
	ActorID           string
	ActorType         string
	ActivityActorType string
	Reminders         reminders.Interface

	// certVerifyCache caches chain-of-trust validity windows for foreign signer
	// certs seen on inbound attestations. Keyed by cert digest (SHA-256(certDER)
	// as string bytes); value is a certValidityWindow carrying the leaf's
	// NotBefore/NotAfter. A workflow calling the same foreign activity/child N
	// times pays chain-of-trust parsing + verification once instead of N times.
	certVerifyCache sync.Map
}

// Reset clears the per-instance cert chain-of-trust cache. Called on
// actor deactivation so no stale trust anchor decisions are held across
// Sentry CA rotation.
func (s *Signing) Reset() {
	s.certVerifyCache.Clear()
}
