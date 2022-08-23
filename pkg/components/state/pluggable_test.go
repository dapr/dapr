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

package state

import (
	"testing"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/components"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"github.com/stretchr/testify/assert"
)

func TestMustLoadStateStore(t *testing.T) {
	l := NewFromPluggable(components.Pluggable{
		Type: components.State,
	})
	assert.NotNil(t, l)
}

//nolint:nosnakecase
func TestMappers(t *testing.T) {
	t.Run("consistencyOf should return unspecified for unknown consistency", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONSISTENCY_UNSPECIFIED, consistencyOf(""))
	})

	t.Run("consistencyOf should return proper consistency when well-known consistency is used", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONSISTENCY_EVENTUAL, consistencyOf("CONSISTENCY_EVENTUAL"))
		assert.Equal(t, v1.StateOptions_CONSISTENCY_STRONG, consistencyOf("CONSISTENCY_STRONG"))
	})

	t.Run("concurrencyOf should return unspecified for unknown concurrency", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONCURRENCY_UNSPECIFIED, concurrencyOf(""))
	})

	t.Run("concurrencyOf should return proper concurrency when well-known concurrency is used", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONCURRENCY_FIRST_WRITE, concurrencyOf("CONCURRENCY_FIRST_WRITE"))
		assert.Equal(t, v1.StateOptions_CONCURRENCY_LAST_WRITE, concurrencyOf("CONCURRENCY_LAST_WRITE"))
	})

	t.Run("toGetRequest should return nil when receiving a nil request", func(t *testing.T) {
		assert.Nil(t, toGetRequest(nil))
	})

	t.Run("toGetRequest should map all properties from the given request", func(t *testing.T) {
		const fakeKey = "fake"
		getRequest := toGetRequest(&state.GetRequest{
			Key: fakeKey,
			Metadata: map[string]string{
				fakeKey: fakeKey,
			},
			Options: state.GetStateOption{
				Consistency: "CONSISTENCY_EVENTUAL",
			},
		})
		assert.Equal(t, getRequest.Key, fakeKey)
		assert.Equal(t, getRequest.Metadata[fakeKey], fakeKey)
		assert.Equal(t, getRequest.Consistency, v1.StateOptions_CONSISTENCY_EVENTUAL)
	})

	t.Run("fromGetResponse should return nil when receiving a nil response", func(t *testing.T) {
		assert.Nil(t, fromGetResponse(nil))
	})
	t.Run("fromGetResponse should map all properties from the given response", func(t *testing.T) {
		fakeData := []byte(`mydata`)
		fakeKey := "key"
		fakeETag := "etag"
		resp := fromGetResponse(&proto.GetResponse{
			Data: fakeData,
			Etag: &v1.Etag{
				Value: fakeETag,
			},
			Metadata: map[string]string{
				fakeKey: fakeKey,
			},
		})
		assert.Equal(t, resp.Data, fakeData)
		assert.Equal(t, resp.ETag, &fakeETag)
		assert.Equal(t, resp.Metadata[fakeKey], fakeKey)
	})

	t.Run("toETagRequest should return nil when receiving a nil etag", func(t *testing.T) {
		assert.Nil(t, toETagRequest(nil))
	})
	t.Run("toETagRequest should set the etag value when receiving a valid etag value", func(t *testing.T) {
		fakeETag := "this"
		etagRequest := toETagRequest(&fakeETag)
		assert.NotNil(t, etagRequest)
		assert.Equal(t, etagRequest.Value, fakeETag)
	})

	t.Run("fromETagResponse should return nil when receiving a nil etag response", func(t *testing.T) {
		assert.Nil(t, fromETagResponse(nil))
	})
	t.Run("fromETagResponse should return the etag value from the response", func(t *testing.T) {})

	t.Run("toDeleteRequest should return nil when receiving a nil delete request", func(t *testing.T) {
		assert.Nil(t, toDeleteRequest(nil))
	})
	t.Run("toDeleteRequest map all properties for the given request", func(t *testing.T) {})

	t.Run("toSetRequest should return nil when receiving a nil set request", func(t *testing.T) {
		req, err := toSetRequest(nil)
		assert.Nil(t, err)
		assert.Nil(t, req)
	})
	t.Run("toSetRequest accept and parse values as []byte", func(t *testing.T) {
		const fakeKey, fakePropValue = "fakeKey", "fakePropValue"
		fakeEtag := "fakeEtag"
		for _, fakeValue := range []any{"fakeStrValue", []byte(`fakeByteValue`), make(map[string]string)} {
			req, err := toSetRequest(&state.SetRequest{
				Key:   fakeKey,
				Value: fakeValue,
				ETag:  &fakeEtag,
				Metadata: map[string]string{
					fakeKey: fakePropValue,
				},
				Options: state.SetStateOption{
					Concurrency: "CONCURRENCY_LAST_WRITE",
					Consistency: "CONSISTENCY_EVENTUAL",
				},
			})
			assert.Nil(t, err)
			assert.NotNil(t, req)
			assert.Equal(t, req.Key, fakeKey)
			assert.NotNil(t, req.Value)
			assert.Equal(t, req.Metadata[fakeKey], fakePropValue)
			assert.Equal(t, req.Options.Concurrency, v1.StateOptions_CONCURRENCY_LAST_WRITE)
			assert.Equal(t, req.Options.Consistency, v1.StateOptions_CONSISTENCY_EVENTUAL)
		}
	})
}
