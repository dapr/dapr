/*
Copyright 2023 The Dapr Authors
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

package internal

// ActorAPILevel is the level of the Actor APIs supported by this runtime.
// It is sent to the Placement service and disseminated to all other Dapr runtimes.
// The Dapr runtime can use this value, as well as the minimum API level observed in the cluster (as disseminated by Placement) to make decisions on feature availability across the cluster.
const ActorAPILevel = 10
