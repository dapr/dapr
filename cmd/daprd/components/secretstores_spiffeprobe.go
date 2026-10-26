//go:build secretstores_spiffeprobe

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

package components

import (
	"context"
	"strconv"
	"sync"

	contribsecret "github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/secretstores/local/env"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/logger"
)

// SpiffeProbeResolutionReportKey is the reserved secret name that reads back
// what the probe observed, instead of recording a new observation.
const SpiffeProbeResolutionReportKey = "__resolution__"

// spiffeProbeSecretStore is an integration-test-only secret store that records
// whether the workload's SPIFFE identity (the X.509 and JWT SVID sources)
// reached the context the runtime resolves secretKeyRef entries on.
//
// Secret resolution happens while a component is being loaded, not on an API
// call, so there is nothing for a test to observe in-band. The store therefore
// records what it saw and reports it back on a later read of the reserved
// SpiffeProbeResolutionReportKey. That read travels the data plane path and
// carries an SVID context of its own, so it is served before anything is
// recorded and can never overwrite what resolution saw.
//
// It is gated behind the secretstores_spiffeprobe build tag, set only for the
// integration-test daprd binary and never for a released flavor, so it is never
// shipped. It embeds the env secret store to satisfy the full
// secretstores.SecretStore interface for free, overriding only GetSecret.
type spiffeProbeSecretStore struct {
	contribsecret.SecretStore

	lock     sync.Mutex
	recorded bool
	hasX509  bool
	hasJWT   bool
}

func (s *spiffeProbeSecretStore) GetSecret(ctx context.Context, req contribsecret.GetSecretRequest) (contribsecret.GetSecretResponse, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if req.Name == SpiffeProbeResolutionReportKey {
		return contribsecret.GetSecretResponse{Data: map[string]string{
			"recorded": strconv.FormatBool(s.recorded),
			"x509":     strconv.FormatBool(s.hasX509),
			"jwt":      strconv.FormatBool(s.hasJWT),
		}}, nil
	}

	_, s.hasX509 = spiffecontext.X509From(ctx)
	_, s.hasJWT = spiffecontext.JWTFrom(ctx)
	s.recorded = true

	// Resolve to a fixed value so the referencing component's metadata is
	// populated and it loads as normal.
	return contribsecret.GetSecretResponse{Data: map[string]string{req.Name: "resolved"}}, nil
}

func init() {
	secretstoresLoader.DefaultRegistry.RegisterComponent(func(log logger.Logger) contribsecret.SecretStore {
		return &spiffeProbeSecretStore{SecretStore: env.NewEnvSecretStore(log)}
	}, "spiffeprobe")
}
