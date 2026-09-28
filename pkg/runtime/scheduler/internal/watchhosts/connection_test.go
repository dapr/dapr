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

package watchhosts

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"

	"github.com/dapr/dapr/pkg/security/fake"
)

func TestConnSchedulerHostsClosesFailedConnection(t *testing.T) {
	// Register only from a serial test: gRPC's resolver registry is global.
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			for attempt := range 10 {
				closed := make(chan struct{})
				r := manual.NewBuilderWithScheme(fmt.Sprintf("watchhosts-cleanup-%t-%d", canceled, attempt))
				r.BuildCallback = func(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) {
					cc.ReportError(errors.New("injected DNS failure"))
				}
				r.CloseCallback = func() { close(closed) }
				resolver.Register(r)

				ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
				t.Cleanup(cancel)
				if canceled {
					cancel()
				}
				w := &WatchHosts{
					allAddrs: []string{r.Scheme() + ":///scheduler"},
					security: fake.New(),
				}
				stream, closeCon, err := w.connSchedulerHosts(ctx)
				require.ErrorContains(t, err, "failed to watch scheduler hosts")
				if !canceled {
					assert.Contains(t, err.Error(), "injected DNS failure")
				}
				assert.Nil(t, stream)
				assert.Nil(t, closeCon)
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("failed WatchHosts attempt did not close its gRPC resolver")
				}
			}
		})
	}
}
