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

package payloadstore

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
)

func testRef(t *testing.T, data string) Reference {
	t.Helper()
	return Reference{
		Checksum: sha256.Sum256([]byte(data)),
		Key:      "test-key/" + data,
		Size:     uint64(len(data)),
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	ref := testRef(t, "some payload")

	encoded := EncodeReference(ref)
	assert.True(t, IsReference(encoded))

	decoded, err := DecodeReference(encoded)
	require.NoError(t, err)
	assert.Equal(t, ref, decoded)
}

func TestIsReferenceRejectsUserData(t *testing.T) {
	t.Parallel()

	encoded := EncodeReference(testRef(t, "x"))

	for name, payload := range map[string]string{
		"empty":                     "",
		"plain text":                "hello world",
		"json":                      `{"k":"a","c":"b","s":1}`,
		"json array":                `[1,2,3]`,
		"leading NUL only":          "\x00hello",
		"leading control chars":     "\x00\x01\x02data",
		"truncated magic":           encoded[:3],
		"ref embedded mid-string":   "prefix" + encoded,
		"magic-like ascii":          "dapr.workflow.payload.reference.v1",
		"control chars not magic":   "\x1f\x1e\x1d\x00\x01\x02",
		"multibyte utf8":            "päylöad ☃ работа",
		"almost magic then diverge": encoded[:8] + "ZZZZZZZZ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, IsReference(payload))

			_, err := DecodeReference(payload)
			require.ErrorIs(t, err, ErrNotReference)
		})
	}
}

func TestDecodeReferenceCorruptBody(t *testing.T) {
	t.Parallel()

	encoded := EncodeReference(testRef(t, "x"))
	// Recover the magic prefix by stripping the JSON body.
	magic := encoded[:strings.IndexByte(encoded, '{')]
	require.NotEmpty(t, magic)

	for name, payload := range map[string]string{
		"magic only":          magic,
		"magic plus garbage":  magic + "not json",
		"magic empty json":    magic + "{}",
		"bad checksum hex":    magic + `{"k":"a","c":"zz","s":1}`,
		"short checksum":      magic + `{"k":"a","c":"abcd","s":1}`,
		"truncated body":      encoded[:len(encoded)-2],
		"trailing bytes":      encoded + "x",
		"checksum wrong type": magic + `{"k":"a","c":42,"s":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// The magic prefix is present, so the cheap detector fires...
			assert.True(t, IsReference(payload))

			// ...but a strict decode must fail, and NOT with ErrNotReference:
			// this is a corrupt/forged reference, not user data.
			_, err := DecodeReference(payload)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrNotReference)
		})
	}
}

// An encoded reference is stored in proto3 string fields
// (wrapperspb.StringValue), which require valid UTF-8. Marshal fails on
// invalid UTF-8, so this round trip proves the encoding is wire-safe.
func TestEncodedReferenceIsValidProtoString(t *testing.T) {
	t.Parallel()

	encoded := EncodeReference(testRef(t, "wire safety"))

	e := &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				Result: wrapperspb.String(encoded),
			},
		},
	}

	data, err := proto.Marshal(e)
	require.NoError(t, err)

	var got protos.HistoryEvent
	require.NoError(t, proto.Unmarshal(data, &got))
	assert.Equal(t, encoded, got.GetTaskCompleted().GetResult().GetValue())
}
