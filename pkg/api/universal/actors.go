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

package universal

import (
	"context"
	"time"
)

// SetActorsInitDone indicates that the actors runtime has been initialized, whether actors are available or not
func (a *Universal) SetActorsInitDone() {
	if a.actorsReady.CompareAndSwap(false, true) {
		close(a.actorsReadyCh)
	}
}

// WaitForActorsReady blocks until the actor runtime is set in the object (or until the context is canceled).
func (a *Universal) WaitForActorsReady(ctx context.Context) {
	// Quick check to avoid allocating a timer if the actors are ready
	if a.actorsReady.Load() {
		return
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()

	// In both cases, it's a no-op as we check for the actors runtime to be ready below
	select {
	case <-waitCtx.Done():
	case <-a.actorsReadyCh:
	}
}
