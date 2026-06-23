//go:build state_spiffeprobe

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
	"encoding/json"

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/logger"
)

// spiffeProbeStore is an integration-test-only state store that reports, via its
// Get response, whether the workload's SPIFFE identity (the X.509 and JWT SVID
// sources) reached the component operation context — letting a test assert the
// injection end-to-end through the real daprd binary.
//
// It is gated behind the state_spiffeprobe build tag, set only for the
// integration-test daprd binary and never for a released flavor, so it is never
// shipped. It embeds the in-memory store to satisfy the full state.Store
// interface for free, overriding only Get.
type spiffeProbeStore struct {
	contribstate.Store
}

// Get reports which SVID sources are present in ctx, ignoring any stored value.
func (s *spiffeProbeStore) Get(ctx context.Context, _ *contribstate.GetRequest) (*contribstate.GetResponse, error) {
	_, hasX509 := spiffecontext.X509From(ctx)
	_, hasJWT := spiffecontext.JWTFrom(ctx)
	data, err := json.Marshal(map[string]bool{"x509": hasX509, "jwt": hasJWT})
	if err != nil {
		return nil, err
	}
	return &contribstate.GetResponse{Data: data}, nil
}

func init() {
	stateLoader.DefaultRegistry.RegisterComponent(func(log logger.Logger) contribstate.Store {
		return &spiffeProbeStore{Store: inmemory.NewInMemoryStateStore(log)}
	}, "spiffeprobe")
}
