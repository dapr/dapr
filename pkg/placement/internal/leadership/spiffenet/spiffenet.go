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

package spiffenet

import (
	"context"
	"net"
	"time"

	"github.com/hashicorp/raft"
	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/dapr/pkg/security"
)

type Options struct {
	PlaceID  spiffeid.ID
	Security security.Handler
	Listener net.Listener
}

type spiffeStreamLayer struct {
	placeID spiffeid.ID
	ctx     context.Context
	sec     security.Handler
	net.Listener
}

func New(ctx context.Context, opts Options) raft.StreamLayer {
	return &spiffeStreamLayer{
		placeID:  opts.PlaceID,
		ctx:      ctx,
		sec:      opts.Security,
		Listener: opts.Listener,
	}
}

// Dial implements the StreamLayer interface.
func (s *spiffeStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	return s.sec.NetDialerID(s.ctx, s.placeID, timeout)("tcp", string(address))
}
