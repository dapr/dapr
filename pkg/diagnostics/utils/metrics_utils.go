/*
Copyright 2023 The Dapr Authors
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
	"regexp"
	"strings"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/config"
)

var metricsRules map[string][]regexPair

type regexPair struct {
	regex   *regexp.Regexp
	replace string
}

// NewMeasureView creates opencensus View instance using stats.Measure.
func NewMeasureView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// WithTags converts tag key and value pairs to tag.Mutator array.
// WithTags(key1, value1, key2, value2) returns
// []tag.Mutator{tag.Upsert(key1, value1), tag.Upsert(key2, value2)}.
func WithTags(name string, opts ...interface{}) []tag.Mutator {
	tagMutators := make([]tag.Mutator, 0, len(opts)/2)
	for i := 0; i < len(opts)-1; i += 2 {
		key, ok := opts[i].(tag.Key)
		if !ok {
			break
		}
		value, ok := opts[i+1].(string)
		if !ok {
			break
		}
		// skip if value is empty
		if value != "" {
			if len(metricsRules) > 0 {
				pairs := metricsRules[strings.ReplaceAll(name, "_", "/")+key.Name()]

				for _, p := range pairs {
					value = p.regex.ReplaceAllString(value, p.replace)
				}
			}

			tagMutators = append(tagMutators, tag.Upsert(key, value))
		}
	}
	return tagMutators
}

// AddNewTagKey adds new tag keys to existing view.
func AddNewTagKey(views []*view.View, key *tag.Key) []*view.View {
	for _, v := range views {
		v.TagKeys = append(v.TagKeys, *key)
	}

	return views
}

// CreateRulesMap generates a fast lookup map for metrics regex.
func CreateRulesMap(rules []config.MetricsRule) error {
	metricsRules = map[string][]regexPair{}

	for _, r := range rules {
		// strip the metric name of known runtime prefixes and mutate them to fit stat names
		r.Name = strings.Replace(r.Name, "dapr_", "", 1)
		r.Name = strings.ReplaceAll(r.Name, "_", "/")

		for _, l := range r.Labels {
			metricsRules[r.Name+l.Name] = make([]regexPair, 0, len(l.Regex))

			for k, v := range l.Regex {
				regex, err := regexp.Compile(v)
				if err != nil {
					return err
				}

				metricsRules[r.Name+l.Name] = append(metricsRules[r.Name+l.Name], regexPair{
					regex:   regex,
					replace: k,
				})
			}
		}
	}

	return nil
}
