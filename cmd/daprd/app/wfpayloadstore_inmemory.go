//go:build wfpayloadstore_inmemory

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

import (
	"os"
	"strconv"

	"github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore"
	payloadstorefake "github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore/fake"
)

// wfPayloadStoreThresholdEnvVar enables the integration-test-only
// in-memory workflow payload store; its value is the offload threshold in
// bytes. It exists only in binaries built with the wfpayloadstore_inmemory
// tag - nothing in a released daprd reads it. Must stay in sync with
// daprd.WithWorkflowPayloadStoreThreshold in
// tests/integration/framework/process/daprd.
const wfPayloadStoreThresholdEnvVar = "DAPR_TEST_WORKFLOW_PAYLOAD_STORE_THRESHOLD"

// workflowPayloadStore returns an in-memory payload store configured with
// the threshold from the environment, so integration tests can exercise
// offloading through the real daprd binary. This variant is compiled only
// under the wfpayloadstore_inmemory build tag, which the integration-test
// harness sets and released daprd flavors never do. With the variable
// unset it returns nil, keeping every test that does not opt in on the
// nil store, identical to a release build.
func workflowPayloadStore() payloadstore.Store {
	v, ok := os.LookupEnv(wfPayloadStoreThresholdEnvVar)
	if !ok {
		return nil
	}

	threshold, err := strconv.Atoi(v)
	if err != nil || threshold <= 0 {
		log.Fatalf("invalid %s value %q: must be a positive integer", wfPayloadStoreThresholdEnvVar, v)
	}

	log.Warn("integration-test-only in-memory workflow payload store enabled")
	return payloadstorefake.New().WithThreshold(threshold)
}
