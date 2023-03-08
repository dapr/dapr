/*
Copyright 2023 The Dapr Authors
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

package signals

import (
	"context"
	"os"
	"os/signal"

	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("dapr.signals")

	// Inspired by
	// https://github.com/kubernetes-sigs/controller-runtime/blob/8499b67e316a03b260c73f92d0380de8cd2e97a1/pkg/manager/signals/signal.go#L25
	onlyOneSignalHandler = make(chan struct{})
)

// Context returns a context which will be canceled when either the SIGINT
// (windows and non windows) or SIGTERM (non windows) signal is caught. If
// either signal is caught a second time, the program is terminated immediately
// with exit code 1.
func Context() context.Context {
	// panics when called twice
	close(onlyOneSignalHandler)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, shutdownSignals...)

	go func() {
		sig := <-sigCh
		log.Infof(`Received signal "%s"; beginning shutdown`, sig)
		cancel()
		sig = <-sigCh
		log.Fatalf(
			`Received signal "%s" during shutdown; exiting immediately`,
			sig,
		)
	}()

	return ctx
}
