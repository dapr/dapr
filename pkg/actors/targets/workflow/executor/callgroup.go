/*
Copyright 2025 The Dapr Authors
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

package executor

import "sync"

// callGroup counts in-flight calls like a WaitGroup, but add may run
// concurrently with wait.
type callGroup struct {
	mu   sync.Mutex
	n    int
	idle chan struct{}
}

func (g *callGroup) add() {
	g.mu.Lock()
	g.n++
	g.mu.Unlock()
}

func (g *callGroup) done() {
	g.mu.Lock()
	g.n--
	if g.n == 0 && g.idle != nil {
		close(g.idle)
		g.idle = nil
	}
	g.mu.Unlock()
}

func (g *callGroup) wait() {
	g.mu.Lock()
	if g.n == 0 {
		g.mu.Unlock()
		return
	}
	if g.idle == nil {
		g.idle = make(chan struct{})
	}
	idle := g.idle
	g.mu.Unlock()
	<-idle
}
