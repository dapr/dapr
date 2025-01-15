// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// NewTimeoutTransport returns a transport created using the given TLS info.
// If read/write on the created connection blocks longer than its time limit,
// it will return timeout error.
// If read/write timeout is set, transport will not be able to reuse connection.
func NewTimeoutTransport(info TLSInfo, dialtimeoutd, rdtimeoutd, wtimeoutd time.Duration) (*http.Transport, error) {
	tr, err := NewTransport(info, dialtimeoutd)
	if err != nil {
		return nil, err
	}

	if rdtimeoutd != 0 || wtimeoutd != 0 {
		// the timed out connection will time out soon after it is idle.
		// it should not be put back to http transport as an idle connection for future usage.
		tr.MaxIdleConnsPerHost = -1
	} else {
		// allow more idle connections between peers to avoid unnecessary port allocation.
		tr.MaxIdleConnsPerHost = 1024
	}

	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		id, err := spiffeid.FromPath(info.Security.ControlPlaneTrustDomain(), "/ns/"+info.Security.ControlPlaneNamespace()+"/"+"dapr-scheduler")
		if err != nil {
			return nil, fmt.Errorf("failed to create SPIFFE ID for server: %w", err)
		}
		dialFunc := info.Security.NetDialerID(ctx, id, dialtimeoutd)
		return dialFunc(network, address)

	}
	return tr, nil
}
