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

package clients

import (
	"context"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// generation is one immutable set of scheduler clients. Borrowers taken via
// Next hold a reference on the generation they borrowed from, so a Reload can
// swap in a new generation without waiting for (possibly hung) in-flight
// calls against the old one.
type generation struct {
	clients  []schedulerv1pb.SchedulerClient
	closeFns []context.CancelFunc
	wg       sync.WaitGroup
}

// close force-closes the generation's connections first, aborting any
// in-flight RPCs (a scheduler that died mid-shutdown can hold streams open
// and ACK keepalives without ever answering, so waiting for borrowers before
// closing can block forever), then waits for borrowers to release.
func (g *generation) close() {
	var wg sync.WaitGroup
	wg.Add(len(g.closeFns))
	for _, closeFn := range g.closeFns {
		go func() {
			closeFn()
			wg.Done()
		}()
	}
	wg.Wait()
	g.wg.Wait()
}
