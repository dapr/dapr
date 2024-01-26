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

package daprd

import (
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/binding"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/hotreload"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/httpserver"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/metadata"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/metrics"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/middleware"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/mtls"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/multipleconfigs"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/outbox"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/pluggable"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/pubsub"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/resiliency"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/resources"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/secret"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/shutdown"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/state"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow"
)
