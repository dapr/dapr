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

// StartReadingFromBindings enables app reads for every binding instance.
type StartReadingFromBindings struct {
	*catbase
	Result chan<- error
}

// StopReadingFromBindings stops every binding instance. Forever marks the
// category as permanently stopped so future Init events do not start reading.
type StopReadingFromBindings struct {
	*catbase
	Forever bool
	Done    chan struct{}
}

// StartInput tells one binding instance to begin reading from the underlying
// input binding.
type StartInput struct {
	*instbase
	Result chan<- error
}

// StopInput tells one binding instance to stop reading.
type StopInput struct {
	*instbase
	Done chan struct{}
}
