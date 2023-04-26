//go:build unit

package diagtestutils

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

// NewTag is a helper to create an opencensus tag that can be used in the different helpers here
func NewTag(key string, value string) tag.Tag {
	return tag.Tag{
		Key:   tag.MustNewKey(key),
		Value: value,
	}
}

// GetValueForObservationWithTagSet is a helper to find a row out of a slice of rows retrieved when executing view.RetrieveData
// This particular row should have the tags present in the tag set.
func GetValueForObservationWithTagSet(rows []*view.Row, wantedTagSetCount map[tag.Tag]bool) int64 {
	for _, row := range rows {
		foundTags := 0
		for _, aTag := range row.Tags {
			if wantedTagSetCount[aTag] {
				foundTags++
			}
		}
		if foundTags == len(wantedTagSetCount) {
			return row.Data.(*view.CountData).Value
		}
	}
	return 0
}

// RequireTagExist tries to find a tag in a slice of rows return from view.RetrieveData
func RequireTagExist(t *testing.T, rows []*view.Row, wantedTag tag.Tag) {
	t.Helper()
	var found bool
outerLoop:
	for _, row := range rows {
		for _, aTag := range row.Tags {
			if reflect.DeepEqual(wantedTag, aTag) {
				found = true
				break outerLoop
			}
		}
	}
	require.True(t, found, fmt.Sprintf("did not find tag (%s) in rows:", wantedTag), rows)
}

func RequireTagNotExist(t *testing.T, rows []*view.Row, wantedTag tag.Tag) {
	t.Helper()
	var found bool
outerLoop:
	for _, row := range rows {
		for _, aTag := range row.Tags {
			if reflect.DeepEqual(wantedTag, aTag) {
				found = true
				break outerLoop
			}
		}
	}
	require.False(t, found, fmt.Sprintf("found tag (%s) in rows:", wantedTag), rows)
}

func CleanupRegisteredViews(viewNames ...string) {
	var views []*view.View
	for _, v := range viewNames {
		if v := view.Find(v); v != nil {
			views = append(views, v)
		}
	}
	view.Unregister(views...)
}
