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

package log

import (
	"strings"
	"sync"
)

type Log struct {
	mu   sync.Mutex
	logs []string
}

func New() *Log {
	return new(Log)
}

func (b *Log) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logs = append(b.logs, string(p))
	return len(p), nil
}

func (b *Log) Close() error {
	return nil
}

func (b *Log) Contains(substr string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, log := range b.logs {
		if strings.Contains(log, substr) {
			return true
		}
	}
	return false
}

func (b *Log) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logs = nil
}
