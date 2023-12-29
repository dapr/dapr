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

package wfbackend

import (
	"time"

	"github.com/dapr/components-contrib/metadata"
	metadataUtil "github.com/dapr/kit/metadata"
	"github.com/microsoft/durabletask-go/backend/sqlite"
)

const (
	defaultFilePath                 = ""
	defaultOrchestrationLockTimeout = 2 * time.Minute
	defaultActivityLockTimeout      = 2 * time.Minute
)

// Metadata represents a set of workflow specific properties.
type Metadata struct {
	metadata.Base `json:",inline"`
}

type sqliteMetadata struct {
	sqlite.SqliteOptions
}

func NewSqliteMetadata() sqliteMetadata {
	return sqliteMetadata{
		SqliteOptions: sqlite.SqliteOptions{
			FilePath:                 defaultFilePath,
			OrchestrationLockTimeout: defaultOrchestrationLockTimeout,
			ActivityLockTimeout:      defaultActivityLockTimeout,
		},
	}
}

func (m *sqliteMetadata) Parse(meta map[string]string) error {
	err := metadataUtil.DecodeMetadata(meta, &m.SqliteOptions)
	if err != nil {
		return err
	}

	return nil
}
