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
	"github.com/dapr/components-contrib/state/cockroachdb"
	pgv2 "github.com/dapr/components-contrib/state/postgresql/v2"
	"github.com/dapr/dapr/pkg/components"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
)

func init() {
	stateLoader.DefaultRegistry.RegisterComponentWithVersions("cockroachdb", components.Versioning{
		Preferred: components.VersionConstructor{
			// For v2, this component uses the same implementation as the postgres state store
			Version: "v2", Constructor: pgv2.NewPostgreSQLStateStore,
		},
		Others: []components.VersionConstructor{
			{Version: "v1", Constructor: cockroachdb.New},
		},
		Default: "v1",
	})
}
