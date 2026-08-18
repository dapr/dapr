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
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/config"
)

var metricsRules map[string][]regexPair

var StaticPaths = map[string]bool{
	"/dapr/config":    true,
	"/dapr/metrics":   true,
	"/dapr/subscribe": true,
	"/healthz":        true,
}

var ValidHTTPVerbs = map[string]bool{
	"GET":     true,
	"PUT":     true,
	"POST":    true,
	"PATCH":   true,
	"DELETE":  true,
	"HEAD":    true,
	"OPTIONS": true,
	"CONNECT": true,
	"TRACE":   true,
}

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
func WithTags(name string, opts ...any) []tag.Mutator {
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
		if value == "" {
			continue
		}

		tagMutators = append(tagMutators, tag.Upsert(key, NormalizeTagValue(name, key, value)))
	}
	return tagMutators
}

// NormalizeTagValue applies the configured metric rules (regex label
// rewrites) for the measure and tag key to value, exactly as the per-record
// WithTags path does.
func NormalizeTagValue(name string, key tag.Key, value string) string {
	if len(metricsRules) == 0 {
		return value
	}
	for _, p := range metricsRules[strings.ReplaceAll(name, "_", "/")+key.Name()] {
		value = p.regex.ReplaceAllString(value, p.replace)
	}
	return value
}

// rulesGeneration counts CreateRulesMap installations. Cached tag maps are
// keyed and built from rule-normalized values, so any cache built under an
// older rules generation must be discarded: production installs rules once
// at startup, so in practice this trips at most once per cache.
var rulesGeneration atomic.Uint64

// CachedTagMaps caches fully-built *tag.Map values keyed by their varying
// tag values, so hot-path metric records can call stats.Recorder.Record
// directly instead of paying the RecordWithOptions chain (mutator closures,
// option closures, a fresh tag map and value context) on every record:
// ~18-20 allocations per record down to zero for cached counters.
//
// Base tags (typically app_id and namespace, constant for the process) are
// applied once per rules generation. Cache keys are the RULE-NORMALIZED
// values, so the cache's cardinality mirrors the exported view's even when
// a cardinality rule collapses many raw values into one label, and a rules
// installation after construction rebuilds the base tags and drops stale
// entries. Intended for low-cardinality exported discriminators (statuses,
// kinds, bounded method sets).
type CachedTagMaps struct {
	measureName string
	base        []any

	mu      sync.Mutex
	gen     atomic.Uint64
	baseCtx atomic.Value // context.Context
	maps    sync.Map     // normalized value-tuple key -> *tag.Map
}

// NewCachedTagMaps builds the cache with constant base tag pairs
// (key1, value1, key2, value2, ...).
func NewCachedTagMaps(measureName string, base ...any) *CachedTagMaps {
	c := &CachedTagMaps{measureName: measureName, base: base}
	c.rebuild(rulesGeneration.Load())
	return c
}

func (c *CachedTagMaps) rebuild(gen uint64) {
	ctx, _ := tag.New(context.Background(), WithTags(c.measureName, c.base...)...)
	c.baseCtx.Store(ctx)
	c.maps.Range(func(k, _ any) bool {
		c.maps.Delete(k)
		return true
	})
	c.gen.Store(gen)
}

// baseContext returns the base-tag context for the current rules
// generation, rebuilding the cache if rules were installed after this
// cache was constructed.
func (c *CachedTagMaps) baseContext() context.Context {
	g := rulesGeneration.Load()
	if c.gen.Load() != g {
		c.mu.Lock()
		if c.gen.Load() != g {
			c.rebuild(g)
		}
		c.mu.Unlock()
	}
	return c.baseCtx.Load().(context.Context)
}

// Get1 returns the cached tag map for one varying tag. Zero allocations on
// the cached path.
func (c *CachedTagMaps) Get1(k tag.Key, v string) *tag.Map {
	base := c.baseContext()
	v = NormalizeTagValue(c.measureName, k, v)
	if m, ok := c.maps.Load(v); ok {
		return m.(*tag.Map)
	}
	ctx := base
	if v != "" {
		ctx, _ = tag.New(base, tag.Upsert(k, v))
	}
	m := tag.FromContext(ctx)
	c.maps.Store(v, m)
	return m
}

// Get2 returns the cached tag map for two varying tags. The composite key
// concatenation allocates one small string per call; use nested caches if
// even that matters.
func (c *CachedTagMaps) Get2(k1 tag.Key, v1 string, k2 tag.Key, v2 string) *tag.Map {
	base := c.baseContext()
	v1 = NormalizeTagValue(c.measureName, k1, v1)
	v2 = NormalizeTagValue(c.measureName, k2, v2)
	key := v1 + "\x00" + v2
	if m, ok := c.maps.Load(key); ok {
		return m.(*tag.Map)
	}
	muts := make([]tag.Mutator, 0, 2)
	if v1 != "" {
		muts = append(muts, tag.Upsert(k1, v1))
	}
	if v2 != "" {
		muts = append(muts, tag.Upsert(k2, v2))
	}
	ctx, _ := tag.New(base, muts...)
	m := tag.FromContext(ctx)
	c.maps.Store(key, m)
	return m
}

// Base returns the tag map holding only the constant base tags, for
// measures with no varying discriminator.
func (c *CachedTagMaps) Base() *tag.Map {
	return tag.FromContext(c.baseContext())
}

// CachedInt64Counter records a constant-increment counter through cached
// tag maps: zero allocations on the cached path.
type CachedInt64Counter struct {
	meter stats.Recorder
	maps  *CachedTagMaps
	m1    []stats.Measurement
	m0    []stats.Measurement
}

// NewCachedInt64Counter builds the counter recorder; base holds the
// constant tag pairs.
func NewCachedInt64Counter(meter stats.Recorder, measure *stats.Int64Measure, base ...any) *CachedInt64Counter {
	return &CachedInt64Counter{
		meter: meter,
		maps:  NewCachedTagMaps(measure.Name(), base...),
		m1:    []stats.Measurement{measure.M(1)},
		m0:    []stats.Measurement{measure.M(0)},
	}
}

// Record1 records +1 with one varying tag.
func (c *CachedInt64Counter) Record1(k tag.Key, v string) {
	c.meter.Record(c.maps.Get1(k, v), c.m1, nil)
}

// Record2 records +1 with two varying tags.
func (c *CachedInt64Counter) Record2(k1 tag.Key, v1 string, k2 tag.Key, v2 string) {
	c.meter.Record(c.maps.Get2(k1, v1, k2, v2), c.m1, nil)
}

// Zero1 records 0 with one varying tag, pre-registering the series so it is
// exported at value 0 before its first increment. Only meaningful for views
// with a Sum aggregation: under Count every record, including a zero, counts.
func (c *CachedInt64Counter) Zero1(k tag.Key, v string) {
	c.meter.Record(c.maps.Get1(k, v), c.m0, nil)
}

// CachedInt64Recorder records variable int64 measurements (byte sizes)
// through cached tag maps: one small slice allocation per record.
type CachedInt64Recorder struct {
	meter   stats.Recorder
	maps    *CachedTagMaps
	measure *stats.Int64Measure
}

// NewCachedInt64Recorder builds the recorder; base holds the constant tag
// pairs.
func NewCachedInt64Recorder(meter stats.Recorder, measure *stats.Int64Measure, base ...any) *CachedInt64Recorder {
	return &CachedInt64Recorder{
		meter:   meter,
		maps:    NewCachedTagMaps(measure.Name(), base...),
		measure: measure,
	}
}

// Record1 records with one varying tag.
func (c *CachedInt64Recorder) Record1(k tag.Key, tv string, v int64) {
	c.meter.Record(c.maps.Get1(k, tv), []stats.Measurement{c.measure.M(v)}, nil)
}

// CachedFloat64Recorder records float64 measurements (histograms, gauges)
// through cached tag maps: one small slice allocation per record.
type CachedFloat64Recorder struct {
	meter   stats.Recorder
	maps    *CachedTagMaps
	measure *stats.Float64Measure
}

// NewCachedFloat64Recorder builds the recorder; base holds the constant tag
// pairs.
func NewCachedFloat64Recorder(meter stats.Recorder, measure *stats.Float64Measure, base ...any) *CachedFloat64Recorder {
	return &CachedFloat64Recorder{
		meter:   meter,
		maps:    NewCachedTagMaps(measure.Name(), base...),
		measure: measure,
	}
}

// Record0 records with only the base tags.
func (c *CachedFloat64Recorder) Record0(v float64) {
	c.meter.Record(c.maps.Base(), []stats.Measurement{c.measure.M(v)}, nil)
}

// Record1 records with one varying tag.
func (c *CachedFloat64Recorder) Record1(k tag.Key, tv string, v float64) {
	c.meter.Record(c.maps.Get1(k, tv), []stats.Measurement{c.measure.M(v)}, nil)
}

// Record2 records with two varying tags.
func (c *CachedFloat64Recorder) Record2(k1 tag.Key, v1 string, k2 tag.Key, v2 string, v float64) {
	c.meter.Record(c.maps.Get2(k1, v1, k2, v2), []stats.Measurement{c.measure.M(v)}, nil)
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
	defer rulesGeneration.Add(1)
	newMetricsRules := make(map[string][]regexPair, len(rules))

	for _, r := range rules {
		// strip the metric name of known runtime prefixes and mutate them to fit stat names
		r.Name = strings.Replace(r.Name, "dapr_", "", 1)
		r.Name = strings.ReplaceAll(r.Name, "_", "/")

		for _, l := range r.Labels {
			key := r.Name + l.Name
			newMetricsRules[key] = make([]regexPair, len(l.Regex))

			i := 0
			for k, v := range l.Regex {
				regex, err := regexp.Compile(v)
				if err != nil {
					return fmt.Errorf("failed to compile regex for rule %s/%s: %w", key, k, err)
				}

				newMetricsRules[key][i] = regexPair{
					regex:   regex,
					replace: k,
				}
				i++
			}
		}
	}

	metricsRules = newMetricsRules
	return nil
}
