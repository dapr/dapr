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

	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/backend/sqlite"

	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/kit/logger"
)

func NewSQLiteBackend(md wfbe.Metadata, log logger.Logger) (backend.Backend, error) {
	sqliteMetadata := &sqliteMetadata{}
	err := sqliteMetadata.Parse(md.Properties)
	if err != nil {
		log.Errorf("Failed to parse SQLite backend metadata; SQLite backend is not initialized: %v", err)
		return nil, fmt.Errorf("failed to parse SQLite backend metadata: %w", err)
	}

	be := sqlite.NewSqliteBackend(&sqliteMetadata.SqliteOptions, log)
	return be, nil
}
