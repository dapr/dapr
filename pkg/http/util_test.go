/*
Copyright 2022 The Dapr Authors
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

package http

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertEventToBytes(t *testing.T) {
	t.Run("serialize json to bytes octet-stream content type", func(t *testing.T) {
		res, err := ConvertEventToBytes(map[string]string{
			"test": "event",
		}, "application/octet-stream")
		require.Error(t, err)
		require.ErrorIs(t, err, errContentTypeMismatch)
		assert.Equal(t, []byte{}, res)
	})

	t.Run("serialize base64 bin to bytes proper content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("dGVzdCBldmVudA==", "application/octet-stream")
		require.NoError(t, err)
		assert.Equal(t, []byte("test event"), res)
	})

	t.Run("serialize string data with octet-stream content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("test event", "application/octet-stream")
		require.Error(t, err)
		t.Log(err)
		assert.Equal(t, []byte{}, res)
	})

	t.Run("serialize json data with text/plain content type", func(t *testing.T) {
		res, err := ConvertEventToBytes(map[string]string{
			"test": "event",
		}, "text/plain")
		require.Error(t, err)
		require.ErrorIs(t, err, errContentTypeMismatch)
		assert.Equal(t, []byte{}, res)
	})

	t.Run("serialize string data with application/json content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("test/plain", "application/json")
		require.NoError(t, err)
		// escape quotes
		assert.Equal(t, []byte("\"test/plain\""), res)
	})

	t.Run("serialize string data with text/plain content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("test event", "text/plain")
		require.NoError(t, err)
		assert.Equal(t, []byte("test event"), res)
	})

	t.Run("serialize string data with application/xml content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("</tag>", "text/plain")
		require.NoError(t, err)
		assert.Equal(t, []byte("</tag>"), res)
	})

	t.Run("serialize string data with wrong content type", func(t *testing.T) {
		res, err := ConvertEventToBytes("</tag>", "image/png")
		require.Error(t, err)
		require.ErrorIs(t, err, errContentTypeMismatch)
		assert.Equal(t, []byte{}, res)
	})

	t.Run("serialize json data with application/json content type", func(t *testing.T) {
		event := map[string]string{
			"test": "json",
		}
		exp, err := json.Marshal(event)
		require.NoError(t, err, "expected no error here")
		res, err := ConvertEventToBytes(event, "application/json")
		require.NoError(t, err)
		assert.Equal(t, exp, res)
	})

	t.Run("serialize cloud event data with application/cloudevents+json content type", func(t *testing.T) {
		event := map[string]string{
			"specversion":     "1.0",
			"type":            "com.dapr.event.sent",
			"source":          "https://the.empire.com/logistics",
			"id":              "a1c8c741-0e7c-46d7-afba-06f02aa9b1fb",
			"pubsubname":      "pubsub",
			"topic":           "deathStarStatus",
			"traceid":         "00-2583c3e5f33c8db92a2ba0e3488a6a63-328e7491ec6678de-01",
			"datacontenttype": "text/plain",
			"data":            "test event",
		}
		exp, err := json.Marshal(event)
		require.NoError(t, err, "expected no error here")
		res, err := ConvertEventToBytes(event, "application/cloudevents+json")
		require.NoError(t, err)
		assert.Equal(t, exp, res)
	})
}
