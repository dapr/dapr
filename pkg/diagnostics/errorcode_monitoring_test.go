package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
)

func TestRecordErrorCode(t *testing.T) {
	t.Run("record single error code", func(t *testing.T) {
		m := newErrorCodeMetrics()
		_ = m.Init("app-id")

		m.RecordErrorCode(errorcodes.ActorInstanceMissing)

		viewData, _ := view.RetrieveData("error_code/count")
		v := view.Find("error_code/count")

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

		viewData, _ := view.RetrieveData("error_code/count")
		v := view.Find("error_code/count")

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
}
