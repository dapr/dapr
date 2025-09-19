/*
Copyright 2024 The Dapr Authors
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

package workflow

import (
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/basic"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/continueasnew"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/listener"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/loadbalance"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/maxconcurrent"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/memory"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/nostatestore"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/reconnect"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/records"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/rerun"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/retries"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/scheduler"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/security"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/starttime"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/taskexecutionid"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/timer"
)
