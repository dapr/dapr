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

package sqlite

import (
	"fmt"
	"time"

	"github.com/microsoft/durabletask-go/backend/sqlite"

	kitmd "github.com/dapr/kit/metadata"
)

type sqliteMetadata struct {
	sqlite.SqliteOptions
}

func (m *sqliteMetadata) Parse(meta map[string]string) error {
	// Reset to default values
	m.reset()

	err := kitmd.DecodeMetadata(meta, &m.SqliteOptions)
	if err != nil {
		return fmt.Errorf("failed to decode metadata: %w", err)
	}

	return nil
}

func (m *sqliteMetadata) reset() {
	m.FilePath = ""
	m.OrchestrationLockTimeout = 2 * time.Minute
	m.ActivityLockTimeout = 2 * time.Minute
}
