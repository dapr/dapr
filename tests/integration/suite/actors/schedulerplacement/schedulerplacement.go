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

// Package schedulerplacement tests actor placement served by the scheduler
// (SchedulerPlacement preview feature).
package schedulerplacement

// featureConfig is the daprd Configuration enabling the SchedulerPlacement
// preview feature.
const featureConfig = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerplacement
spec:
  features:
  - name: SchedulerPlacement
    enabled: true
`
