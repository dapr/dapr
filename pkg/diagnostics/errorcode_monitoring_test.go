/*
Copyright 2024 The Dapr Authors
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

package diagnostics

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/codes"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

func TestRecordErrorCode(t *testing.T) {
	t.Run("record single error code", func(t *testing.T) {
		m := newErrorCodeMetrics()
		_ = m.Init("app-id")

		m.RecordErrorCode(errorcodes.ActorInstanceMissing)

		viewData, _ := view.RetrieveData("error_code/total")
		v := view.Find("error_code/total")

		allTagsPresent(t, v, viewData[0].Tags)
		assert.Len(t, viewData, 1)

		// ActorInstanceMissing
		assert.Equal(t, int64(1), viewData[0].Data.(*view.CountData).Value)
		assert.True(t, TagAndValuePresent(viewData[0].Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.ActorInstanceMissing.Code}))
		assert.True(t, TagAndValuePresent(viewData[0].Tags, tag.Tag{Key: categoryKey, Value: "actor"}))
	})

	t.Run("record two valid error codes", func(t *testing.T) {
		m := newErrorCodeMetrics()
		_ = m.Init("app-id")

		m.RecordErrorCode(errorcodes.StateBulkGet)
		m.RecordErrorCode(errorcodes.StateBulkGet)
		m.RecordErrorCode(errorcodes.CommonAPIUnimplemented)

		viewData, _ := view.RetrieveData("error_code/total")
		v := view.Find("error_code/total")

		allTagsPresent(t, v, viewData[0].Tags)

		for _, metric := range viewData {
			if TagAndValuePresent(metric.Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.StateBulkGet.Code}) {
				assert.Equal(t, int64(2), metric.Data.(*view.CountData).Value)
				assert.True(t, TagAndValuePresent(metric.Tags, tag.Tag{Key: categoryKey, Value: "state"}))
			} else if TagAndValuePresent(metric.Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.CommonAPIUnimplemented.Code}) {
				assert.Equal(t, int64(1), metric.Data.(*view.CountData).Value)
				assert.True(t, TagAndValuePresent(metric.Tags, tag.Tag{Key: categoryKey, Value: "common"}))
			}
		}
	})

	t.Run("record different error structures", func(t *testing.T) {
		_ = DefaultErrorCodeMonitoring.Init("app-id")

		assert.True(t, RecordErrorCode(&errorcodes.WorkflowComponentMissing))
		assert.True(t, RecordErrorCode(messages.NewAPIErrorHTTP("error message", errorcodes.WorkflowComponentMissing, 502)))
		assert.True(t, RecordErrorCode(&messages.ErrCryptoGetKey))
		assert.True(
			t,
			RecordErrorCode(errors.NewBuilder(
				codes.InvalidArgument,
				http.StatusBadRequest,
				"test-message",
				"",
				string(errorcodes.CategoryCrypto),
			).WithErrorInfo(errorcodes.CryptoKey.Code, nil).Build(),
			),
		)
		assert.True(t, RecordErrorCode(apierrors.PubSub("pubsub-name").WithMetadata(nil).NotFound()))

		viewData, _ := view.RetrieveData("error_code/total")
		v := view.Find("error_code/total")

		allTagsPresent(t, v, viewData[0].Tags)

		for _, metric := range viewData {
			if TagAndValuePresent(metric.Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.WorkflowComponentMissing.Code}) {
				assert.Equal(t, int64(2), metric.Data.(*view.CountData).Value)
				assert.True(t, TagAndValuePresent(metric.Tags, tag.Tag{Key: categoryKey, Value: string(errorcodes.CategoryWorkflow)}))
			} else if TagAndValuePresent(metric.Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.CryptoKey.Code}) {
				assert.Equal(t, int64(2), metric.Data.(*view.CountData).Value)
				assert.True(t, TagAndValuePresent(metric.Tags, tag.Tag{Key: categoryKey, Value: string(errorcodes.CategoryCrypto)}))
			} else if TagAndValuePresent(metric.Tags, tag.Tag{Key: errorCodeKey, Value: errorcodes.PubSubNotFound.Code}) {
				assert.Equal(t, int64(1), metric.Data.(*view.CountData).Value)
				assert.True(t, TagAndValuePresent(metric.Tags, tag.Tag{Key: categoryKey, Value: string(errorcodes.CategoryPubsub)}))
			}
		}
	})
}
