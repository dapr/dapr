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
//
// API levels per Dapr version:
// - 1.11.x and older = unset (equivalent to 0)
// - 1.12.x = 10
// - 1.13.x = 20
const ActorAPILevel = 20

// Features that can be enabled depending on the API level
type apiLevelFeature uint32

const (
	// APILevelFeatureRemindersProtobuf Enables serializing reminders as protobuf rather than JSON in the pkg/actors/reminders package
	// When serialized as protobuf, reminders have the "\0pb" prefix
	// Note this only control serializations; when un-serializing, legacy JSON is always supported as fallback
	APILevelFeatureRemindersProtobuf apiLevelFeature = 20
)

// IsEnabled returns true if the feature is enabled for the current API level.
func (a apiLevelFeature) IsEnabled(currentAPILevel uint32) bool {
	return currentAPILevel >= uint32(a)
}
