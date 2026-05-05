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
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// TCP wraps net.Listen("tcp", addr) with a short retry on transient EADDRINUSE
// during in-process rebind. After Listener.Close() returns the kernel can
// still hold the bind for a few hundred milliseconds while accepted
// connections drain. Particularly visible on Windows during SIGHUP-driven
// runtime restart cycles, where the new runtime's net.Listen races the old
// listener's teardown.
func TCP(ctx context.Context, addr string) (net.Listener, error) {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 50 * time.Millisecond
	bo.MaxInterval = 200 * time.Millisecond
	bo.MaxElapsedTime = 2 * time.Second

	return backoff.RetryWithData(func() (net.Listener, error) {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, backoff.Permanent(err)
		}
		return nil, err
	}, backoff.WithContext(bo, ctx))
}
