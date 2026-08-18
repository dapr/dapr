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

package utils

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/config"
)

func TestWithTags(t *testing.T) {
	t.Run("one tag", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		mutators := WithTags("", appKey, "test")
		assert.Len(t, mutators, 1)
	})

	t.Run("two tags", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		mutators := WithTags("", appKey, "test", operationKey, "op")
		assert.Len(t, mutators, 2)
	})

	t.Run("three tags", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		methodKey := tag.MustNewKey("method")
		mutators := WithTags("", appKey, "test", operationKey, "op", methodKey, "method")
		assert.Len(t, mutators, 3)
	})

	t.Run("two tags with wrong value type", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		mutators := WithTags("", appKey, "test", operationKey, 1)
		assert.Len(t, mutators, 1)
	})

	t.Run("skip empty value key", func(t *testing.T) {
		appKey := tag.MustNewKey("app_id")
		operationKey := tag.MustNewKey("operation")
		methodKey := tag.MustNewKey("method")
		mutators := WithTags("", appKey, "", operationKey, "op", methodKey, "method")
		assert.Len(t, mutators, 2)
	})
}

func TestCreateRulesMap(t *testing.T) {
	t.Run("invalid rule", func(t *testing.T) {
		err := CreateRulesMap([]config.MetricsRule{
			{
				Name: "test",
				Labels: []config.MetricLabel{
					{
						Name: "test",
						Regex: map[string]string{
							"TEST": "[",
						},
					},
				},
			},
		})
		require.Error(t, err)
	})

	t.Run("valid rule", func(t *testing.T) {
		err := CreateRulesMap([]config.MetricsRule{
			{
				Name: "test",
				Labels: []config.MetricLabel{
					{
						Name: "label",
						Regex: map[string]string{
							"TEST": "/.+",
						},
					},
				},
			},
		})

		require.NoError(t, err)
		assert.NotNil(t, metricsRules)
		assert.Len(t, metricsRules, 1)
		assert.Len(t, metricsRules["testlabel"], 1)
		assert.Equal(t, "TEST", metricsRules["testlabel"][0].replace)
		assert.NotNil(t, metricsRules["testlabel"][0].regex)
	})
}

func TestCachedTagMaps_NormalizedKeysCollapse(t *testing.T) {
	// A cardinality rule collapsing every workflow name into one label must
	// collapse the cache the same way: keys are normalized values, so cache
	// cardinality mirrors the exported view, not the raw input space.
	require.NoError(t, CreateRulesMap([]config.MetricsRule{{
		Name: "wf_count",
		Labels: []config.MetricLabel{{
			Name:  "workflow",
			Regex: map[string]string{"other": "wf-.+"},
		}},
	}}))
	t.Cleanup(func() { require.NoError(t, CreateRulesMap(nil)) })

	wfKey := tag.MustNewKey("workflow")
	c := NewCachedTagMaps("wf_count", tag.MustNewKey("app_id"), "app")
	for i := range 100 {
		m := c.Get1(wfKey, fmt.Sprintf("wf-%d", i))
		v, ok := m.Value(wfKey)
		require.True(t, ok)
		assert.Equal(t, "other", v)
	}
	entries := 0
	c.maps.Range(func(_, _ any) bool { entries++; return true })
	assert.Equal(t, 1, entries, "cache must be keyed by normalized values")
}

func TestCachedTagMaps_RulesInstalledAfterConstruction(t *testing.T) {
	// Production InitMetrics installs rules before any cached recorder is
	// built, but the cache must also self-heal if rules land later: base
	// tags and cached entries are rebuilt under the new rules generation.
	require.NoError(t, CreateRulesMap(nil))
	t.Cleanup(func() { require.NoError(t, CreateRulesMap(nil)) })

	appKey := tag.MustNewKey("app_id")
	stKey := tag.MustNewKey("status")
	c := NewCachedTagMaps("wf_count", appKey, "app-1234")
	m := c.Get1(stKey, "success")
	v, ok := m.Value(appKey)
	require.True(t, ok)
	require.Equal(t, "app-1234", v)

	require.NoError(t, CreateRulesMap([]config.MetricsRule{{
		Name: "wf_count",
		Labels: []config.MetricLabel{{
			Name:  "app_id",
			Regex: map[string]string{"app": "app-.+"},
		}},
	}}))

	m = c.Get1(stKey, "success")
	v, ok = m.Value(appKey)
	require.True(t, ok)
	assert.Equal(t, "app", v, "base tags must be re-normalized under the new rules")
}
