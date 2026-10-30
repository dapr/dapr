/*
Copyright 2026 The Dapr Authors
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

package pubsub

import (
	"encoding/json"
	"fmt"
	"strconv"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/kit/logger"
)

const (
	// DeadLetterErrorDetailsMetadataKey enables attaching app error details to
	// dead-letter publishes when the Pubsub.DeadLetterErrorDetails feature is on.
	DeadLetterErrorDetailsMetadataKey = "deadLetterErrorDetails"
	// DeadLetterErrorDetailsMaxLenMetadataKey optionally overrides the max
	// number of bytes persisted for error details (default 1024).
	DeadLetterErrorDetailsMaxLenMetadataKey = "deadLetterErrorDetailsMaxLen"
	// DefaultDeadLetterErrorDetailsMaxLen is the default truncation limit.
	DefaultDeadLetterErrorDetailsMaxLen = 1024

	// CloudEventErrorDetailsField is the CloudEvent extension attribute used
	// when republishing a dead-letter message that is a CloudEvent.
	CloudEventErrorDetailsField = "errordetails"
	// RawErrorMetadataKey is the publish metadata/header key used for raw
	// payload dead-letter messages (e.g. Kafka headers).
	RawErrorMetadataKey = "__error"

	truncatedSuffix = "...[truncated]"
)

var deadLetterLog = logger.NewLogger("dapr.runtime.pubsub.deadletter")

// DeadLetterErrorDetailsConfig controls whether and how app error details are
// attached when publishing to a dead-letter topic.
type DeadLetterErrorDetailsConfig struct {
	Enabled bool
	MaxLen  int
}

// DeadLetterErrorDetailsConfigFromMetadata returns the DLQ error-details
// configuration. Both the preview feature flag and subscription metadata
// deadLetterErrorDetails=true must be set for Enabled to be true.
func DeadLetterErrorDetailsConfigFromMetadata(meta map[string]string, featureEnabled bool) DeadLetterErrorDetailsConfig {
	cfg := DeadLetterErrorDetailsConfig{
		MaxLen: DefaultDeadLetterErrorDetailsMaxLen,
	}
	if !featureEnabled {
		return cfg
	}

	if val, ok := meta[DeadLetterErrorDetailsMetadataKey]; ok && val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			deadLetterLog.Warnf("invalid %s metadata value %q: %v", DeadLetterErrorDetailsMetadataKey, val, err)
		} else {
			cfg.Enabled = enabled
		}
	}

	if val, ok := meta[DeadLetterErrorDetailsMaxLenMetadataKey]; ok && val != "" {
		maxLen, err := strconv.Atoi(val)
		if err != nil || maxLen <= 0 {
			deadLetterLog.Warnf("invalid %s metadata value %q: using default %d", DeadLetterErrorDetailsMaxLenMetadataKey, val, DefaultDeadLetterErrorDetailsMaxLen)
		} else {
			cfg.MaxLen = maxLen
		}
	}

	return cfg
}

// TruncateErrorDetails truncates details to maxLen bytes, appending a marker
// when truncated. Empty details or non-positive maxLen return details unchanged
// (non-positive maxLen falls back to the default).
func TruncateErrorDetails(details string, maxLen int) string {
	if details == "" {
		return ""
	}
	if maxLen <= 0 {
		maxLen = DefaultDeadLetterErrorDetailsMaxLen
	}
	if len(details) <= maxLen {
		return details
	}
	if maxLen <= len(truncatedSuffix) {
		return details[:maxLen]
	}
	return details[:maxLen-len(truncatedSuffix)] + truncatedSuffix
}

// EnrichDeadLetterRequest attaches error details to a dead-letter publish
// request when configured. For CloudEvents, sets the errordetails extension on
// the event body. For raw payloads, sets the __error metadata key.
func EnrichDeadLetterRequest(req *contribpubsub.PublishRequest, details string, rawPayload bool, cfg DeadLetterErrorDetailsConfig) {
	if req == nil || !cfg.Enabled || details == "" {
		return
	}

	details = TruncateErrorDetails(details, cfg.MaxLen)
	if details == "" {
		return
	}

	if rawPayload {
		req.Metadata = cloneMetadata(req.Metadata)
		req.Metadata[RawErrorMetadataKey] = details
		return
	}

	enriched, err := enrichCloudEventData(req.Data, details)
	if err != nil {
		deadLetterLog.Warnf("failed to attach errordetails to dead-letter CloudEvent: %v", err)
		return
	}
	req.Data = enriched
}

// EnrichDeadLetterBulkEntry attaches error details to a single bulk-publish
// entry destined for a dead-letter topic.
func EnrichDeadLetterBulkEntry(entry *contribpubsub.BulkMessageEntry, details string, rawPayload bool, cfg DeadLetterErrorDetailsConfig) {
	if entry == nil || !cfg.Enabled || details == "" {
		return
	}

	details = TruncateErrorDetails(details, cfg.MaxLen)
	if details == "" {
		return
	}

	if rawPayload {
		entry.Metadata = cloneMetadata(entry.Metadata)
		entry.Metadata[RawErrorMetadataKey] = details
		return
	}

	enriched, err := enrichCloudEventData(entry.Event, details)
	if err != nil {
		deadLetterLog.Warnf("failed to attach errordetails to dead-letter CloudEvent entry %s: %v", entry.EntryId, err)
		return
	}
	entry.Event = enriched
}

func enrichCloudEventData(data []byte, details string) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty cloud event payload")
	}

	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	envelope[CloudEventErrorDetailsField] = details
	return json.Marshal(envelope)
}

func cloneMetadata(meta map[string]string) map[string]string {
	out := make(map[string]string, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	return out
}
