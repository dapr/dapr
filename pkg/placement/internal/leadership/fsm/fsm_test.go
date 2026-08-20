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

package fsm

import (
	"io"
	"strings"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApply(t *testing.T) {
	t.Parallel()

	f := New()
	assert.False(t, f.StoodDown())

	f.Apply(&raft.Log{Data: []byte("something else")})
	assert.False(t, f.StoodDown())

	f.Apply(&raft.Log{Data: StandDownCommand})
	assert.True(t, f.StoodDown())
}

type sinkBuffer struct {
	strings.Builder
	closed bool
}

func (s *sinkBuffer) Close() error  { s.closed = true; return nil }
func (s *sinkBuffer) Cancel() error { return nil }
func (s *sinkBuffer) ID() string    { return "test" }

func TestSnapshotRestore(t *testing.T) {
	t.Parallel()

	t.Run("stood down survives snapshot and restore", func(t *testing.T) {
		t.Parallel()
		f := New()
		f.Apply(&raft.Log{Data: StandDownCommand})

		snap, err := f.Snapshot()
		require.NoError(t, err)
		sink := new(sinkBuffer)
		require.NoError(t, snap.Persist(sink))
		assert.True(t, sink.closed)

		restored := New()
		require.NoError(t, restored.Restore(io.NopCloser(strings.NewReader(sink.String()))))
		assert.True(t, restored.StoodDown())
	})

	t.Run("not stood down restores as not stood down", func(t *testing.T) {
		t.Parallel()
		f := New()
		snap, err := f.Snapshot()
		require.NoError(t, err)
		sink := new(sinkBuffer)
		require.NoError(t, snap.Persist(sink))

		restored := New()
		require.NoError(t, restored.Restore(io.NopCloser(strings.NewReader(sink.String()))))
		assert.False(t, restored.StoodDown())
	})

	t.Run("restore replaces earlier stood down state", func(t *testing.T) {
		t.Parallel()
		f := New()
		f.Apply(&raft.Log{Data: StandDownCommand})
		require.True(t, f.StoodDown())

		require.NoError(t, f.Restore(io.NopCloser(strings.NewReader(""))))
		assert.False(t, f.StoodDown(), "the snapshot is authoritative")
	})
}
