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

package listen

import (
	"context"
	"net"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// TCP wraps net.Listen("tcp", addr) with a retry budget around bind
// failures. After Listener.Close() returns, the kernel can still hold the
// bind for hundreds of milliseconds while accepted connections drain. Most
// visible on Windows during SIGHUP-driven runtime restart cycles, where
// the new runtime's net.Listen races the old listener's teardown of its
// accepted connections.
//
// The platform-specific control hook (see listen_*.go) sets
// SO_REUSEADDR-equivalent options where they help, and the retry covers
// any remaining transient failure window. Real conflicts (port held by a
// different process for the whole budget) still surface.
func TCP(ctx context.Context, addr string) (net.Listener, error) {
	lc := net.ListenConfig{Control: control}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 500 * time.Millisecond
	bo.MaxElapsedTime = 10 * time.Second

	return backoff.RetryWithData(func() (net.Listener, error) {
		return lc.Listen(ctx, "tcp", addr)
	}, backoff.WithContext(bo, ctx))
}
