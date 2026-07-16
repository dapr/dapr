## Highlights

Dapr pub/sub in v1.17 delivers sub-millisecond publish latency at 1,000 QPS on gRPC, with the Bulk Publish API providing dramatic throughput improvements for high-volume scenarios. All test runs completed with **100% success rate** and zero pod restarts.

**How to read these numbers:** Latency percentiles (p50/p90/p99) show the distribution of individual publish times — p50 is the typical experience, p99 captures the rare slow requests. "16 connections" means 16 concurrent publishers hitting the sidecar simultaneously at 1,000 QPS. "Dapr overhead" is measured by comparing the proxied result against a baseline that bypasses Dapr entirely.

**Standard publish — gRPC** (16 connections, 1,000 QPS):
- Median (p50): **0.60 ms** | p90: **0.94 ms** | p99: **1.87 ms**
- 60,000 publish operations — 100% success

With 60,000 publishes at 1,000 QPS, the Dapr sidecar received each message and forwarded it to the broker in a median of 0.60 ms. Nine in ten requests completed in under 1 ms. Every publish succeeded with zero errors.

**Standard publish — HTTP**:
- Median (p50): **0.35 ms** | p95: **0.49 ms** — 100% success

HTTP publish latency is even lower than gRPC in this configuration, with 95% of requests completing in under 0.5 ms. The tight distribution means publish latency through Dapr is nearly constant regardless of load.

**Bulk Publish API — Kafka (gRPC)**: the most impactful scenario for high-throughput systems:
- Single-message publish: **91 QPS** → Bulk publish: **200 QPS** — **2.2× throughput improvement**
- Latency reduced by **138.11 ms at p50** and **223.50 ms at p90** vs. single-message publishing

Without bulk publish, Kafka's per-message broker overhead capped throughput at ~91 QPS. The Bulk Publish API batches multiple messages into a single broker write, unlocking the full 200 QPS target. This isn't just a throughput win — p50 latency dropped by 138.11 ms per message, meaning each individual message also arrives significantly faster when sent in bulk.

**Bulk Publish API — In-memory (gRPC)** (cloud event, 1 KB payload):
- Median: **10.79 ms** | p90: **17.19 ms** — with latency up to **47.22 ms lower** than single-message

Even for in-memory brokers (no network hop to an external broker), bulk publish reduces per-message latency by 47.22 ms at the median compared to publishing messages one at a time. The gain comes from amortizing the per-call overhead of the Dapr API across the batch.

**Bulk Publish HTTP** (memory broker, batch of 10, 1 KB):
- **13,635 iterations/sec** throughput, p95: **8.70 ms** — 100% success

HTTP bulk publish at batch=10 achieves over 13,000 requests per second — 3.7× the throughput of single-message HTTP publish — with 95% of requests completing in under 9 ms.

---

## HTTP

### TestPubsubPublishHttpPerformance

<img src="http/TestPubsubPublishHttpPerformance_data_volume.png" alt="TestPubsubPublishHttpPerformance_data_volume.png" />
<img src="http/TestPubsubPublishHttpPerformance_duration_breakdown.png" alt="TestPubsubPublishHttpPerformance_duration_breakdown.png" />
<img src="http/TestPubsubPublishHttpPerformance_duration_low.png" alt="TestPubsubPublishHttpPerformance_duration_low.png" />
<img src="http/TestPubsubPublishHttpPerformance_summary.png" alt="TestPubsubPublishHttpPerformance_summary.png" />
<img src="http/TestPubsubPublishHttpPerformance_tail_latency.png" alt="TestPubsubPublishHttpPerformance_tail_latency.png" />
<img src="http/TestPubsubPublishHttpPerformance_throughput.png" alt="TestPubsubPublishHttpPerformance_throughput.png" />

---------------

## gRPC

### TestPubsubPublishGrpcPerformance

<img src="grpc/TestPubsubPublishGrpcPerformance_duration_breakdown.png" alt="TestPubsubPublishGrpcPerformance_duration_breakdown.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_duration_requested_vs_actual.png" alt="TestPubsubPublishGrpcPerformance_duration_requested_vs_actual.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_histogram_count.png" alt="TestPubsubPublishGrpcPerformance_histogram_count.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_histogram_percent.png" alt="TestPubsubPublishGrpcPerformance_histogram_percent.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_qps.png" alt="TestPubsubPublishGrpcPerformance_qps.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_resource_cpu.png" alt="TestPubsubPublishGrpcPerformance_resource_cpu.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_resource_memory.png" alt="TestPubsubPublishGrpcPerformance_resource_memory.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_summary.png" alt="TestPubsubPublishGrpcPerformance_summary.png" />
<img src="grpc/TestPubsubPublishGrpcPerformance_tail_latency.png" alt="TestPubsubPublishGrpcPerformance_tail_latency.png" />

---------------

## Bulk

### HTTP

### TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus

<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk

<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal

<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk

<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal

<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_variants_duration

<img src="bulk/http/TestPubsubBulkPublishHttpPerformance_variants_duration.png" alt="TestPubsubBulkPublishHttpPerformance_variants_duration.png" />

### TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus

<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="bulk/http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" />


### gRPC

### TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event

<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)

<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event

<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)

<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" />
<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_variants_duration

<img src="bulk/grpc/TestBulkPubsubPublishGrpcPerformance_variants_duration.png" alt="TestBulkPubsubPublishGrpcPerformance_variants_duration.png" />
