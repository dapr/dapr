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

	"k8s.io/utils/clock"
)

func NewFailure(fails map[string]int, timeouts map[string]time.Duration, callCount map[string]int) Failure {
	return newFailureWithClock(fails, timeouts, callCount, &clock.RealClock{})
}

func newFailureWithClock(fails map[string]int, timeouts map[string]time.Duration, callCount map[string]int, clock clock.Clock) Failure {
	return Failure{
		fails:     fails,
		timeouts:  timeouts,
		callCount: callCount,
		lock:      &sync.RWMutex{},
		clock:     clock,
	}
}

type Failure struct {
	fails     map[string]int
	timeouts  map[string]time.Duration
	callCount map[string]int
	lock      *sync.RWMutex
	clock     clock.Clock
}

func (f *Failure) PerformFailure(key string) error {
	f.lock.Lock()

	f.callCount[key]++

	if v, ok := f.fails[key]; ok {
		if v > 0 {
			f.fails[key]--
			f.lock.Unlock()
			return errors.New("forced failure")
		}
		delete(f.fails, key)
		f.lock.Unlock()
		return nil
	}
	f.lock.Unlock()

	if val, ok := f.timeouts[key]; ok {
		f.clock.Sleep(val)
	}
	return nil
}

func (f *Failure) CallCount(key string) int {
	f.lock.RLock()
	defer f.lock.RUnlock()
	return f.callCount[key]
}
