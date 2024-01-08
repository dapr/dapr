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

package integration

import (
	_ "github.com/dapr/dapr/tests/integration/suite/actors"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/operator"
	_ "github.com/dapr/dapr/tests/integration/suite/placement"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
	_ "github.com/dapr/dapr/tests/integration/suite/sentry"
)
