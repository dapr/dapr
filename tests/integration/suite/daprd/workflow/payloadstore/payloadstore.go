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

// Package payloadstore exercises workflow payload offloading end to end
// through the real daprd binary, covering every user-facing point where a
// payload can enter workflow history. The store only exists in daprd
// binaries built with the wfpayloadstore_inmemory tag, which the
// integration harness sets and released daprd flavors never do, and is
// enabled per process via daprd.WithWorkflowPayloadStoreThreshold, so the
// feature has no surface in stock builds at all.
package payloadstore
