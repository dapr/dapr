/*
Copyright 2021 The Dapr Authors
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

package testing

import (
	"errors"
	"sync"
	"time"
)

type Failure struct {
	Fails     map[string]int
	Timeouts  map[string]time.Duration
	CallCount map[string]int
	lock      *sync.Mutex
}

func (f *Failure) PerformFailure(key string) error {
	if f.lock == nil {
		f.lock = &sync.Mutex{}
	}
	f.lock.Lock()
	f.CallCount[key]++
	f.lock.Unlock()
	if val, ok := f.Fails[key]; ok {
		if val > 0 {
			f.lock.Lock()
			f.Fails[key]--
			f.lock.Unlock()
			return errors.New("forced failure")
		}
		delete(f.Fails, key)
		return nil
	}

	if val, ok := f.Timeouts[key]; ok {
		time.Sleep(val)
	}
	return nil
}
