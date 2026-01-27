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

package leadership

import (
	"context"
	"net"
	"time"
)

const (
	nameResolveRetryInterval = time.Second / 2
	nameResolveMaxRetry      = 240
)

func (s *Leadership) resolveRaftAdvertiseAddr(ctx context.Context, bindAddr string) (*net.TCPAddr, error) {
	// HACKHACK: Kubernetes POD DNS A record population takes some time
	// to look up the address after StatefulSet POD is deployed.
	var err error
	var addr *net.TCPAddr
	for range nameResolveMaxRetry {
		addr, err = net.ResolveTCPAddr("tcp", bindAddr)
		if err == nil {
			return addr, nil
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(nameResolveRetryInterval):
			// nop
		}
	}
	return nil, err
}
