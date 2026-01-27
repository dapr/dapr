# Protobuf CloudEvents Implementation Plan

**Issue:** [dapr/dapr#4541](https://github.com/dapr/dapr/issues/4541)
**Branch:** `protobuf-cloudevents`
**Status:** Implemented (Option 1)

## Implementation Summary (Option 1)

**Completed:** 2026-01-27

### Files Created
- `dapr/proto/cloudevents/v1/cloudevents.proto` - CloudEvents proto definition (Dapr-namespaced)
- `pkg/proto/cloudevents/v1/cloudevents.pb.go` - Generated Go code
- `pkg/runtime/pubsub/cloudevents_proto.go` - Serialization/deserialization library
- `pkg/runtime/pubsub/cloudevents_proto_test.go` - Unit tests

### Files Modified
- `pkg/runtime/subscription/subscription.go` - Added protobuf deserialization in subscribe path
- `pkg/api/http/http.go` - Added protobuf serialization in publish path

### Key Implementation Notes
- Proto package changed from `io.cloudevents.v1` to `dapr.proto.cloudevents.v1` to avoid conflicts with official CloudEvents SDK-Go
- Protobuf format triggered via metadata key `cloudeventsFormat=protobuf`
- Binary data stored natively (no base64 overhead on wire)
- Full backward compatibility maintained - JSON remains the default

### Usage
To publish with protobuf CloudEvents format, include metadata:
```json
{
  "cloudeventsFormat": "protobuf"
}
```

---

## Problem Statement

Dapr currently wraps pub/sub messages in JSON CloudEvents:

```json
{
    "specversion": "1.0",
    "type": "com.dapr.event.sent",
    "topic": "topic",
    "pubsubname": "mqtt-pubsub",
    "traceid": "00-7191a3b376c1c5ee01e71a8f233d8e13-d4d3e178ea8a376f-01",
    "data_base64": "CtMo==",
    "id": "5d4c0961-8923-4646-9010-5f56105a2325",
    "source": "app",
    "datacontenttype": "application/octet-stream"
}
```

**Issues with current approach:**
1. Binary data requires base64 encoding (33% size overhead)
2. JSON serialization is slower than protobuf
3. Non-Dapr tools reading from broker need two-step deserialization
4. No native support for typed protobuf payloads

**Goal:** Support [CloudEvents Protobuf Format](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/protobuf-format.md) with content type `application/cloudevents+protobuf`.

---

## Implementation Options

| Option | Scope | SDK Changes | Complexity | Wire Efficiency |
|--------|-------|-------------|------------|-----------------|
| **1. Wire Format Only** | Broker messages | None | Low | Full |
| 2. End-to-End Proto | API + Wire | All SDKs | High | Full |
| 3. Component Config | Wire + Config | None | Medium | Full |
| 4. Subscription Negotiation | API + Wire | Minor | Medium | Full |

**Recommendation:** Start with Option 1, then evolve to Option 2.

---

## Option 1: Wire Format Only (Detailed Plan)

### Overview

Change only the broker wire format while maintaining full backward compatibility. Apps continue to receive the existing `TopicEventRequest` protobuf - only the message format on the broker changes.

```
┌─────────────────────────────────────────────────────────────────┐
│                     CURRENT FLOW                                │
├─────────────────────────────────────────────────────────────────┤
│  App → Sidecar → [JSON CloudEvent] → Broker → [JSON] → Sidecar │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     NEW FLOW (Option 1)                         │
├─────────────────────────────────────────────────────────────────┤
│  App → Sidecar → [Proto CloudEvent] → Broker → [Proto] → Sidecar│
│                                                                 │
│  Note: App still receives TopicEventRequest (unchanged)         │
└─────────────────────────────────────────────────────────────────┘
```

### Prerequisites

- [x] components-contrib PR #2619 merged (adds `application/cloudevents+protobuf` content type support)

### Implementation Tasks

#### Task 1: Add CloudEvents Proto Definition

**Files to create:**
- `dapr/proto/cloudevents/v1/cloudevents.proto`

**Action:** Vendor the official CloudEvents proto definition.

```protobuf
// dapr/proto/cloudevents/v1/cloudevents.proto
syntax = "proto3";

package io.cloudevents.v1;

import "google/protobuf/any.proto";
import "google/protobuf/timestamp.proto";

option go_package = "github.com/dapr/dapr/pkg/proto/cloudevents/v1;cloudevents";

message CloudEvent {
  // Required Attributes
  string id = 1;
  string source = 2;       // URI-reference
  string spec_version = 3;
  string type = 4;

  // Optional & Extension Attributes
  map<string, CloudEventAttributeValue> attributes = 5;

  // Data (oneof)
  oneof data {
    bytes binary_data = 6;
    string text_data = 7;
    google.protobuf.Any proto_data = 8;
  }
}

message CloudEventAttributeValue {
  oneof attr {
    bool ce_boolean = 1;
    int32 ce_integer = 2;
    string ce_string = 3;
    bytes ce_bytes = 4;
    string ce_uri = 5;
    string ce_uri_ref = 6;
    google.protobuf.Timestamp ce_timestamp = 7;
  }
}

message CloudEventBatch {
  repeated CloudEvent events = 1;
}
```

**Acceptance criteria:**
- Proto compiles without errors
- Generated Go code in `pkg/proto/cloudevents/v1/`

---

#### Task 2: Create CloudEvent Proto Serializer

**Files to create:**
- `pkg/runtime/pubsub/cloudevents_proto.go`
- `pkg/runtime/pubsub/cloudevents_proto_test.go`

**Action:** Implement conversion between internal CloudEvent map and proto format.

```go
// pkg/runtime/pubsub/cloudevents_proto.go
package pubsub

import (
    "google.golang.org/protobuf/proto"
    "google.golang.org/protobuf/types/known/anypb"
    "google.golang.org/protobuf/types/known/timestamppb"

    cepb "github.com/dapr/dapr/pkg/proto/cloudevents/v1"
)

const (
    ContentTypeCloudEventProtobuf = "application/cloudevents+protobuf"
)

// CloudEventToProto converts internal CloudEvent map to protobuf format
func CloudEventToProto(ce map[string]interface{}) (*cepb.CloudEvent, error) {
    protoEvent := &cepb.CloudEvent{
        Id:          getStringField(ce, "id"),
        Source:      getStringField(ce, "source"),
        SpecVersion: getStringField(ce, "specversion"),
        Type:        getStringField(ce, "type"),
        Attributes:  make(map[string]*cepb.CloudEventAttributeValue),
    }

    // Map standard optional attributes
    if v, ok := ce["datacontenttype"]; ok {
        protoEvent.Attributes["datacontenttype"] = &cepb.CloudEventAttributeValue{
            Attr: &cepb.CloudEventAttributeValue_CeString{CeString: v.(string)},
        }
    }

    // Map extension attributes (excluding standard fields)
    standardFields := map[string]bool{
        "id": true, "source": true, "specversion": true, "type": true,
        "data": true, "data_base64": true, "datacontenttype": true,
    }
    for k, v := range ce {
        if standardFields[k] {
            continue
        }
        if attr := toAttributeValue(v); attr != nil {
            protoEvent.Attributes[k] = attr
        }
    }

    // Handle data field
    if data, ok := ce["data"]; ok {
        switch d := data.(type) {
        case []byte:
            protoEvent.Data = &cepb.CloudEvent_BinaryData{BinaryData: d}
        case string:
            protoEvent.Data = &cepb.CloudEvent_TextData{TextData: d}
        default:
            // JSON-serialize complex types
            jsonBytes, _ := json.Marshal(d)
            protoEvent.Data = &cepb.CloudEvent_TextData{TextData: string(jsonBytes)}
        }
    } else if dataB64, ok := ce["data_base64"]; ok {
        decoded, _ := base64.StdEncoding.DecodeString(dataB64.(string))
        protoEvent.Data = &cepb.CloudEvent_BinaryData{BinaryData: decoded}
    }

    return protoEvent, nil
}

// ProtoToCloudEvent converts protobuf CloudEvent to internal map format
func ProtoToCloudEvent(protoEvent *cepb.CloudEvent) (map[string]interface{}, error) {
    ce := map[string]interface{}{
        "id":          protoEvent.Id,
        "source":      protoEvent.Source,
        "specversion": protoEvent.SpecVersion,
        "type":        protoEvent.Type,
    }

    // Map attributes back
    for k, v := range protoEvent.Attributes {
        ce[k] = fromAttributeValue(v)
    }

    // Handle data
    switch d := protoEvent.Data.(type) {
    case *cepb.CloudEvent_BinaryData:
        ce["data_base64"] = base64.StdEncoding.EncodeToString(d.BinaryData)
    case *cepb.CloudEvent_TextData:
        ce["data"] = d.TextData
    case *cepb.CloudEvent_ProtoData:
        // Store as Any for typed access
        ce["data"] = d.ProtoData
    }

    return ce, nil
}

// SerializeCloudEventProto marshals CloudEvent to protobuf bytes
func SerializeCloudEventProto(ce map[string]interface{}) ([]byte, error) {
    protoEvent, err := CloudEventToProto(ce)
    if err != nil {
        return nil, err
    }
    return proto.Marshal(protoEvent)
}

// DeserializeCloudEventProto unmarshals protobuf bytes to CloudEvent map
func DeserializeCloudEventProto(data []byte) (map[string]interface{}, error) {
    protoEvent := &cepb.CloudEvent{}
    if err := proto.Unmarshal(data, protoEvent); err != nil {
        return nil, err
    }
    return ProtoToCloudEvent(protoEvent)
}
```

**Acceptance criteria:**
- Round-trip conversion preserves all fields
- Binary data handled without base64 on wire
- Extension attributes preserved
- Unit tests for all data types

---

#### Task 3: Integrate Proto Serialization in Publish Path

**Files to modify:**
- `pkg/runtime/pubsub/cloudevents.go`
- `pkg/runtime/pubsub/publish.go` (or equivalent publish handler)

**Action:** When content type is `application/cloudevents+protobuf`, serialize using proto instead of JSON.

```go
// In publish handling code
func (p *PubSub) Publish(req *PublishRequest) error {
    // ... existing CloudEvent creation ...

    var data []byte
    var err error

    contentType := req.DataContentType
    if contentType == ContentTypeCloudEventProtobuf {
        data, err = SerializeCloudEventProto(cloudEvent)
    } else {
        // Default: JSON serialization (existing behavior)
        data, err = json.Marshal(cloudEvent)
        contentType = "application/cloudevents+json"
    }

    if err != nil {
        return err
    }

    return p.component.Publish(&pubsub.PublishRequest{
        Data:        data,
        PubsubName:  req.PubsubName,
        Topic:       req.Topic,
        Metadata:    req.Metadata,
        ContentType: contentType,
    })
}
```

**Acceptance criteria:**
- Publishing with `application/cloudevents+protobuf` produces valid proto bytes
- Default behavior (JSON) unchanged
- Metadata/tracing preserved

---

#### Task 4: Integrate Proto Deserialization in Subscribe Path

**Files to modify:**
- `pkg/runtime/subscription/subscription.go`
- `pkg/runtime/pubsub/subscriptions.go`

**Action:** Detect protobuf content type and deserialize accordingly.

```go
// In subscription.go - message handling
func (s *Subscription) handleMessage(msg *pubsub.NewMessage) error {
    var cloudEvent map[string]interface{}
    var err error

    contentType := msg.ContentType
    if contentType == "" {
        contentType = msg.Metadata["content-type"]
    }

    switch contentType {
    case ContentTypeCloudEventProtobuf, "application/cloudevents+protobuf":
        cloudEvent, err = DeserializeCloudEventProto(msg.Data)
    case "application/cloudevents+json", "":
        // Existing JSON handling
        err = json.Unmarshal(msg.Data, &cloudEvent)
    default:
        // Raw payload handling (existing)
        cloudEvent = contribpubsub.FromRawPayload(msg.Data, msg.Topic, msg.Metadata)
    }

    if err != nil {
        return fmt.Errorf("failed to deserialize cloud event: %w", err)
    }

    // Continue with existing TopicEventRequest creation...
    return s.deliverMessage(cloudEvent, msg)
}
```

**Acceptance criteria:**
- Protobuf messages from broker correctly deserialized
- Converted to standard `TopicEventRequest` for app delivery
- JSON messages continue to work (backward compatibility)
- Content type detection works via header or metadata

---

#### Task 5: Add Integration Tests

**Files to create:**
- `tests/integration/suite/pubsub/cloudevents_proto_test.go`

**Test scenarios:**
1. Publish proto CloudEvent, subscribe and verify data integrity
2. Publish JSON, subscribe proto-capable subscriber (interop)
3. Binary data round-trip without base64 overhead
4. Extension attributes preserved
5. Tracing context preserved (`traceparent`, `tracestate`)
6. Bulk publish/subscribe with proto format

**Acceptance criteria:**
- All test scenarios pass
- No regressions in existing JSON tests

---

#### Task 6: Documentation

**Files to create/modify:**
- `docs/development/pubsub-cloudevents-protobuf.md`

**Content:**
- How to enable protobuf CloudEvents
- Content type specification
- Performance comparison (size, latency)
- Migration guide from JSON
- Interoperability notes

---

### Implementation Sequence

```
Task 1 (Proto Definition)
    │
    ▼
Task 2 (Serializer) ──────┐
    │                     │
    ▼                     ▼
Task 3 (Publish)     Task 4 (Subscribe)
    │                     │
    └──────────┬──────────┘
               │
               ▼
         Task 5 (Tests)
               │
               ▼
         Task 6 (Docs)
```

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Content type detection fails | Check both header and metadata; add fallback |
| Broker doesn't preserve content type | Store content type in message metadata |
| Breaking existing subscribers | Default to JSON; proto only when explicitly requested |
| Tracing fields non-standard | Map `traceid`/`traceparent` to CloudEvent extensions |

---

## Option 2: End-to-End Protobuf CloudEvent (Summary)

Extend `TopicEventRequest` to include native CloudEvent proto field.

**Proto changes:**
```protobuf
message TopicEventRequest {
  // ... existing fields ...

  // Native CloudEvent proto (new field)
  io.cloudevents.v1.CloudEvent cloud_event = 11;
}
```

**Benefits:**
- Apps receive typed `google.protobuf.Any` payloads
- Full CloudEvent spec compliance
- Path to deprecate legacy fields

**Effort:** High - requires SDK updates for all languages (Go, Python, Java, .NET, JS, Rust, PHP, C++)

**Dependency:** Option 1 should be completed first.

---

## Option 3: Component Configuration (Summary)

Add pubsub component metadata to select CloudEvent format.

```yaml
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mqtt-pubsub
spec:
  type: pubsub.mqtt3
  metadata:
    - name: cloudeventsFormat
      value: "protobuf"  # "json" (default) | "protobuf"
```

**Benefits:**
- Per-component control
- No API changes
- Gradual migration path

**Effort:** Medium - component config parsing + validation

**Consideration:** May cause format mismatch between services using different configurations.

---

## Option 4: Subscription-Level Format Negotiation (Summary)

Let subscribers declare preferred CloudEvent format.

```protobuf
message TopicSubscription {
  // ... existing fields ...

  // Preferred CloudEvent format for delivery
  string cloudevents_format = 8;  // "json" | "protobuf"
}
```

**Benefits:**
- Subscriber controls format
- Different consumers can coexist
- Clean API extension

**Effort:** Medium - subscription handling + potential format conversion

**Consideration:** Publisher may store in different format than subscriber wants, requiring sidecar conversion.

---

## Success Metrics

1. **Wire size reduction:** 30%+ smaller messages for binary payloads
2. **Serialization performance:** 2-3x faster than JSON (benchmark)
3. **Backward compatibility:** 100% existing tests pass
4. **Adoption:** Used by at least one production workload within 3 months

---

## References

- [CloudEvents Protobuf Format Spec](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/protobuf-format.md)
- [Official cloudevents.proto](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/formats/cloudevents.proto)
- [Dapr Issue #4541](https://github.com/dapr/dapr/issues/4541)
- [Components-contrib PR #2619](https://github.com/dapr/components-contrib/pull/2619) (merged)
- [Current TopicEventRequest](https://github.com/dapr/dapr/blob/master/dapr/proto/runtime/v1/appcallback.proto)
