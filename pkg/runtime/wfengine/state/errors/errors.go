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

package errors

// ErrorTypeHistoryTampered is the well-known FailureDetails.ErrorType set on
// workflows that are marked failed because their persisted history or signing
// state was detected as tampered. Clients can match on this constant to
// programmatically detect the failure mode.
const ErrorTypeHistoryTampered = "DAPR_WORKFLOW_HISTORY_TAMPERED"

// VerificationError is returned by LoadWorkflowState when the persisted
// workflow state has been tampered with: signature chain verification has
// failed, signing material is missing, metadata bounds are exceeded, or
// inbox events do not match signed history. The recovery action is to mark
// the workflow as FAILED via [state.MarkAsTamperFailed].
//
// The State (when available) is returned alongside this error so callers
// can scope the tombstone-write transaction to the existing keys.
type VerificationError struct {
	err error
}

func NewVerificationError(err error) *VerificationError {
	return &VerificationError{err}
}

func (e *VerificationError) Error() string {
	return e.err.Error()
}

func (e *VerificationError) Unwrap() error {
	return e.err
}

// ConfigurationError is returned by LoadWorkflowState when the workflow
// state is intact but cannot be loaded under the host's current
// configuration. This covers the one-way signing commitment (a signed
// workflow on a non-signing host, or vice versa) and missing app ID
// configuration. The workflow data is not tampered, so the tombstone
// recovery path must not run; the operator is expected to fix the config.
type ConfigurationError struct {
	err error
}

func NewConfigurationError(err error) *ConfigurationError {
	return &ConfigurationError{err}
}

func (e *ConfigurationError) Error() string {
	return e.err.Error()
}

func (e *ConfigurationError) Unwrap() error {
	return e.err
}
