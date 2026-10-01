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

package metrics

import (
	"sync"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

type Meter struct {
	view.Meter

	lock    sync.RWMutex
	started bool
	stopped bool
}

func NewMeter() *Meter {
	return &Meter{Meter: view.NewMeter()}
}

func (m *Meter) Start() {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.started {
		return
	}
	m.started = true
	m.Meter.Start()
}

func (m *Meter) Stop() {
	m.lock.Lock()
	defer m.lock.Unlock()
	if !m.started || m.stopped {
		return
	}
	m.stopped = true
	m.Meter.Stop()
}

func (m *Meter) Record(tags *tag.Map, measurement any, attachments map[string]any) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	if m.stopped {
		return
	}
	m.Meter.Record(tags, measurement, attachments)
}
