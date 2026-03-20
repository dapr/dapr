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

package state

// VerificationError is returned by LoadWorkflowState when signature
// verification fails. The State is still returned alongside this error so
// callers can build failure metadata from it.
type VerificationError struct {
	Err error
}

func (e *VerificationError) Error() string {
	return e.Err.Error()
}

func (e *VerificationError) Unwrap() error {
	return e.Err
}
