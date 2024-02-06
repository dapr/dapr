package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/config"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

func TestRegexRulesSingle(t *testing.T) {
	const statName = "test_stat_regex"
	methodKey := tag.MustNewKey("method")
	testStat := stats.Int64(statName, "Stat used in unit test", stats.UnitDimensionless)

	InitMetrics("testAppId2", "", []config.MetricsRule{
		{
			Name: statName,
			Labels: []config.MetricLabel{
				{
					Name: methodKey.Name(),
					Regex: map[string]string{
						"/orders/TEST":      "/orders/.+",
						"/lightsabers/TEST": "/lightsabers/.+",
					},
				},
			},
		},
	}, false)

	t.Run("single regex rule applied", func(t *testing.T) {
		view.Register(
			diagUtils.NewMeasureView(testStat, []tag.Key{methodKey}, defaultSizeDistribution),
		)
		t.Cleanup(func() {
			view.Unregister(view.Find(statName))
		})

		stats.RecordWithTags(context.Background(),
			diagUtils.WithTags(testStat.Name(), methodKey, "/orders/123"),
			testStat.M(1))

		viewData, _ := view.RetrieveData(statName)
		v := view.Find(statName)

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, "/orders/TEST", viewData[0].Tags[0].Value)
	})

	t.Run("single regex rule not applied", func(t *testing.T) {
		view.Register(
			diagUtils.NewMeasureView(testStat, []tag.Key{methodKey}, defaultSizeDistribution),
		)
		t.Cleanup(func() {
			view.Unregister(view.Find(statName))
		})

		s := newGRPCMetrics()
		s.Init("test")

		stats.RecordWithTags(context.Background(),
			diagUtils.WithTags(testStat.Name(), methodKey, "/siths/123"),
			testStat.M(1))

		viewData, _ := view.RetrieveData(statName)
		v := view.Find(statName)

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, "/siths/123", viewData[0].Tags[0].Value)
	})

	t.Run("correct regex rules applied", func(t *testing.T) {
		view.Register(
			diagUtils.NewMeasureView(testStat, []tag.Key{methodKey}, defaultSizeDistribution),
		)
		t.Cleanup(func() {
			view.Unregister(view.Find(statName))
		})

		s := newGRPCMetrics()
		s.Init("test")

		stats.RecordWithTags(context.Background(),
			diagUtils.WithTags(testStat.Name(), methodKey, "/orders/123"),
			testStat.M(1))
		stats.RecordWithTags(context.Background(),
			diagUtils.WithTags(testStat.Name(), methodKey, "/lightsabers/123"),
			testStat.M(1))

		viewData, _ := view.RetrieveData(statName)

		orders := false
		lightsabers := false

		for _, v := range viewData {
			if v.Tags[0].Value == "/orders/TEST" {
				orders = true
			} else if v.Tags[0].Value == "/lightsabers/TEST" {
				lightsabers = true
			}
		}

		assert.True(t, orders)
		assert.True(t, lightsabers)
	})
}
