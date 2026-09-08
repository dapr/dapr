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
	"os"
	"sync"
	"time"

	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.workflow.common")

// defaultJanitorPeriod is the per-instance janitor backstop reminder's repeat
// interval. It bounds the worst-case recovery latency for an inbox row or an
// in-flight activity whose local drive AND escalation were both lost.
const defaultJanitorPeriod = 20 * time.Second

// EnvDurationOr returns the positive duration parsed from the named
// environment variable, or def. The env overrides exist for integration
// tests that exercise time-driven behavior without waiting production
// intervals; they are not supported production knobs.
func EnvDurationOr(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Warnf("Ignoring invalid %s %q", name, v)
	}
	return def
}

// JanitorPeriod resolves the janitor repeat interval once per process.
var JanitorPeriod = sync.OnceValue(func() time.Duration {
	return EnvDurationOr("DAPR_WORKFLOW_JANITOR_PERIOD", defaultJanitorPeriod)
})
