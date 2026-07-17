//go:build !wfpayloadstore_inmemory

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

package app

import "github.com/dapr/durabletask-go/backend/payloadstore"

// workflowPayloadStore returns nil in every released daprd flavor:
// without the wfpayloadstore_inmemory build tag there is no way to enable
// workflow payload offloading - no store, no flag, no environment
// variable. The integration-test binary compiles the in-memory variant
// instead.
func workflowPayloadStore() payloadstore.Store { return nil }
