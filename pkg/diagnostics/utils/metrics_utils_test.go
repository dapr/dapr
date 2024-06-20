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
