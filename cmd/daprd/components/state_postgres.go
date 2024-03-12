//go:build allcomponents || stablecomponents

/*
Copyright 2021 The Dapr Authors
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

package components

import (
	pgv1 "github.com/dapr/components-contrib/state/postgresql/v1"
	pgv2 "github.com/dapr/components-contrib/state/postgresql/v2"
	"github.com/dapr/dapr/pkg/components"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
)

func init() {
	// Note: for v2, cockroachdb uses the same implementation as postgres, but it's defined in state_cockroachdb.go
	stateLoader.DefaultRegistry.RegisterComponentWithVersions("postgres", components.Versioning{
		Preferred: components.VersionConstructor{
			Version: "v2", Constructor: pgv2.NewPostgreSQLStateStore,
		},
		Others: []components.VersionConstructor{
			{Version: "v1", Constructor: pgv1.NewPostgreSQLStateStore},
		},
		Default: "v1",
	})

	stateLoader.DefaultRegistry.RegisterComponentWithVersions("postgresql", components.Versioning{
		Preferred: components.VersionConstructor{
			Version: "v2", Constructor: pgv2.NewPostgreSQLStateStore,
		},
		Others: []components.VersionConstructor{
			{Version: "v1", Constructor: pgv1.NewPostgreSQLStateStore},
		},
		Default: "v1",
	})

	// The v2 component can also be used with YugabyteDB
	// There's no "v1" for YugabyteDB
	stateLoader.DefaultRegistry.RegisterComponentWithVersions("yugabytedb", components.Versioning{
		Preferred: components.VersionConstructor{
			Version: "v2", Constructor: pgv2.NewPostgreSQLStateStore,
		},
		Default: "v2",
	})
	stateLoader.DefaultRegistry.RegisterComponentWithVersions("yugabyte", components.Versioning{
		Preferred: components.VersionConstructor{
			Version: "v2", Constructor: pgv2.NewPostgreSQLStateStore,
		},
		Default: "v2",
	})
}
