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

package framework

import (
	"testing"
)

type stdwriter struct {
	t *testing.T
}

func newStdWriter(t *testing.T) *stdwriter {
	return &stdwriter{t}
}

func (w *stdwriter) Write(p []byte) (n int, err error) {
	w.t.Log(w.t.Name() + ": " + string(p))
	return len(p), nil
}

func (w *stdwriter) Close() error {
	return nil
}
