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

package processor

import (
	"context"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// Tests processComponentAndDependents with a randomized
// component
func FuzzProcessComponentsAndDependents(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fdp := fuzz.NewConsumer(data)
		comp := &componentsapi.Component{}
		fdp.GenerateStruct(comp)
		proc, _ := newTestProc()
		proc.processComponentAndDependents(context.Background(), *comp)
	})
}
