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

package common

import (
	"github.com/dapr/dapr/pkg/config"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// PriorityLevels maps a configured level name to its scheduler level.
var PriorityLevels = map[string]schedulerv1pb.PriorityLevel{
	config.PriorityLevelCritical:   schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL,
	config.PriorityLevelHigh:       schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_HIGH,
	config.PriorityLevelNormal:     schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL,
	config.PriorityLevelLow:        schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW,
	config.PriorityLevelBackground: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND,
}
