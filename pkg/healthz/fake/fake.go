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

package fake

import (
	"sync/atomic"
)

type Fake struct {
	readyCalled   atomic.Bool
	unreadyCalled atomic.Bool
}

func New() *Fake {
	return &Fake{}
}

func (f *Fake) Ready()    { f.readyCalled.Store(true) }
func (f *Fake) NotReady() { f.unreadyCalled.Store(true) }

func (f *Fake) ReadyCalled() bool   { return f.readyCalled.Load() }
func (f *Fake) UnreadyCalled() bool { return f.unreadyCalled.Load() }
