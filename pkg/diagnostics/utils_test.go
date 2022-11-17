package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

func allTagsPresent(t *testing.T, v *view.View, tags []tag.Tag) {
	for _, k := range v.TagKeys {
		found := false

		if k.Name() == "" {
			continue
		}

		for _, tag := range tags {
			if tag.Key.Name() == "" {
				continue
			}

			if k.Name() == tag.Key.Name() {
				found = true
				break
			}
		}

		assert.True(t, found)
	}
}
