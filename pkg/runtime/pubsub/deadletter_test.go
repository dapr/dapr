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

package pubsub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
)

func TestDeadLetterErrorDetailsConfigFromMetadata(t *testing.T) {
	t.Run("disabled when feature is off", func(t *testing.T) {
		cfg := DeadLetterErrorDetailsConfigFromMetadata(map[string]string{
			DeadLetterErrorDetailsMetadataKey: "true",
		}, false)
		assert.False(t, cfg.Enabled)
		assert.Equal(t, DefaultDeadLetterErrorDetailsMaxLen, cfg.MaxLen)
	})

	t.Run("requires subscription metadata opt-in", func(t *testing.T) {
		cfg := DeadLetterErrorDetailsConfigFromMetadata(nil, true)
		assert.False(t, cfg.Enabled)
	})

	t.Run("enabled with feature and metadata", func(t *testing.T) {
		cfg := DeadLetterErrorDetailsConfigFromMetadata(map[string]string{
			DeadLetterErrorDetailsMetadataKey:       "true",
			DeadLetterErrorDetailsMaxLenMetadataKey: "64",
		}, true)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, 64, cfg.MaxLen)
	})

	t.Run("invalid max len keeps default", func(t *testing.T) {
		cfg := DeadLetterErrorDetailsConfigFromMetadata(map[string]string{
			DeadLetterErrorDetailsMetadataKey:       "true",
			DeadLetterErrorDetailsMaxLenMetadataKey: "nope",
		}, true)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, DefaultDeadLetterErrorDetailsMaxLen, cfg.MaxLen)
	})
}

func TestTruncateErrorDetails(t *testing.T) {
	assert.Empty(t, TruncateErrorDetails("", 10))
	assert.Equal(t, "short", TruncateErrorDetails("short", 10))

	long := strings.Repeat("a", 50)
	got := TruncateErrorDetails(long, 20)
	assert.Len(t, got, 20)
	assert.True(t, strings.HasSuffix(got, truncatedSuffix))
}

func TestEnrichDeadLetterRequest(t *testing.T) {
	ce, err := json.Marshal(map[string]any{
		"specversion": "1.0",
		"id":          "1",
		"type":        "test",
		"source":      "test",
		"data":        map[string]any{"hello": "world"},
	})
	require.NoError(t, err)

	t.Run("no-op when disabled or empty", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{Data: append([]byte(nil), ce...)}
		EnrichDeadLetterRequest(req, "boom", false, DeadLetterErrorDetailsConfig{})
		assert.Equal(t, ce, req.Data)

		EnrichDeadLetterRequest(req, "", false, DeadLetterErrorDetailsConfig{Enabled: true})
		assert.Equal(t, ce, req.Data)
	})

	t.Run("cloud event errordetails extension", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{Data: append([]byte(nil), ce...)}
		EnrichDeadLetterRequest(req, "processing failed", false, DeadLetterErrorDetailsConfig{
			Enabled: true,
			MaxLen:  DefaultDeadLetterErrorDetailsMaxLen,
		})

		var envelope map[string]any
		require.NoError(t, json.Unmarshal(req.Data, &envelope))
		assert.Equal(t, "processing failed", envelope[CloudEventErrorDetailsField])
	})

	t.Run("raw payload __error metadata", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{
			Data:     []byte("raw"),
			Metadata: map[string]string{"key": "value"},
		}
		EnrichDeadLetterRequest(req, "processing failed", true, DeadLetterErrorDetailsConfig{
			Enabled: true,
			MaxLen:  DefaultDeadLetterErrorDetailsMaxLen,
		})
		assert.Equal(t, "processing failed", req.Metadata[RawErrorMetadataKey])
		assert.Equal(t, "value", req.Metadata["key"])
		assert.Equal(t, []byte("raw"), req.Data)
	})

	t.Run("truncates details", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{
			Data:     []byte("raw"),
			Metadata: map[string]string{},
		}
		details := strings.Repeat("x", 100)
		EnrichDeadLetterRequest(req, details, true, DeadLetterErrorDetailsConfig{
			Enabled: true,
			MaxLen:  32,
		})
		assert.Len(t, req.Metadata[RawErrorMetadataKey], 32)
	})
}

func TestMessageError(t *testing.T) {
	err := NewMessageDroppedError("bad payload")
	require.ErrorIs(t, err, ErrMessageDropped)
	assert.Equal(t, "bad payload", ErrorDetails(err))

	assert.NoError(t, NewMessageError(nil, ""))
	assert.Empty(t, ErrorDetails(assert.AnError))
}
