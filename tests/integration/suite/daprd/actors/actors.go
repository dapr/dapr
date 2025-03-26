/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actors

import (
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/call"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/deactivation"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/grpc"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/http"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/lock"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/metadata"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/reminders"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/state"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/actors/timers"
)
