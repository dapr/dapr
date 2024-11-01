package diagnostics

import (
	"testing"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
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
		assert.Equal(t, errorcodes.ActorInstanceMissing, viewData[0].Tags[1].Value)
		assert.Equal(t, "actor", viewData[0].Tags[2].Value)
	})

	t.Run("record two valid error codes and ignore one invalid", func(t *testing.T) {
		m := newErrorCodeMetrics()
		_ = m.Init("app-id")

		m.RecordErrorCode(errorcodes.StateBulkGet)
		m.RecordErrorCode(errorcodes.StateBulkGet)
		m.RecordErrorCode(errorcodes.CommonAPIUnimplemented)
		m.RecordErrorCode("invalid-error-code")

		viewData, _ := view.RetrieveData("error_code/count")
		v := view.Find("error_code/count")

		allTagsPresent(t, v, viewData[0].Tags)

		for i := 0; i < len(viewData); i++ {
			metric1 := viewData[i]
			switch metric1.Tags[1].Value {
			case errorcodes.StateBulkGet:
				assert.Equal(t, int64(2), viewData[i].Data.(*view.CountData).Value)
				assert.Equal(t, "state", viewData[i].Tags[2].Value)
			case errorcodes.CommonAPIUnimplemented:
				assert.Equal(t, int64(1), viewData[i].Data.(*view.CountData).Value)
				assert.Equal(t, "common", viewData[i].Tags[2].Value)
			case "invalid-error-code":
				t.FailNow()
			}
		}
	})
}
