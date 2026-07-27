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

package loops

import (
	"time"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// Init asks the named instance to initialise a component. Result receives one
// error and is buffered cap 1. Internal is set when the root loop
// re-enqueues a dependent component after its parent secret store comes
// online; it suppresses double counting in the root's in-flight counter.
// Timeout bounds the actual component init on the instance loop; when zero the
// instance applies no deadline.
type Init struct {
	*rootbase
	*catbase
	*instbase
	Component compapi.Component
	Result    chan<- error
	Internal  bool
	Timeout   time.Duration
}

// Close asks the named instance to close a component. Result receives one
// error and is buffered cap 1.
type Close struct {
	*rootbase
	*catbase
	*instbase
	Component compapi.Component
	Result    chan<- error
}
