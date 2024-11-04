/*
Copyright 2024 The Dapr Authors
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

package apilevel

import "sync/atomic"

type APILevel struct {
	level atomic.Uint32
}

func New() *APILevel {
	return new(APILevel)
}

func (a *APILevel) Set(level uint32) {
	a.level.Store(level)
}

func (a *APILevel) Get() uint32 {
	return a.level.Load()
}
