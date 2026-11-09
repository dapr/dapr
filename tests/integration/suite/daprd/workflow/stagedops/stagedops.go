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

// Package stagedops records what each workflow operation does while no
// workflow worker is connected to any daprd of the target app, with the
// WorkflowsStagedOperations preview off and on.
//
// Each file covers one operation with one case. The case runs one cluster
// with daprds of the preview off next to daprds of it on, each of its own
// app, and runs the same steps against each mode as the subtests off and on.
// The assertions record the current behavior, which is not always the wanted
// one: a change in behavior fails the test, and whoever makes the change
// updates the expected outcome here, so the table these tests prove stays
// true.
package stagedops
