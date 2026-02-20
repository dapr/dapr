## Highlights

The Bulk Publish API significantly reduces per-message overhead by batching multiple messages into a single API call. In v1.17, all bulk publish test variants passed with **100% success rate** and zero pod restarts.

**How to read these numbers:** The "latency reduction" figures show how much faster each individual message arrives with bulk publish compared to sending messages one at a time. Kafka shows the largest gains because single-message Kafka writes involve per-broker-write network round-trips and acknowledgments — bulk publish amortizes that cost across each batch. In-memory brokers show smaller but still meaningful gains from amortizing the Dapr API call overhead itself.

**gRPC — Kafka with cloud event** (the highest-impact scenario):
- Bulk API achieves **200 QPS** vs. **91 QPS** single-message — **2.2× throughput improvement**
- Latency reduced by **138.11 ms at p50**, **223.50 ms at p90**, **174.29 ms at p99**

Kafka's per-message commit overhead was throttling single-message publish to 91 QPS. Batching multiple messages into one broker write breaks that bottleneck entirely, reaching the full 200 QPS target. Each individual message also arrives faster because the broker commit cost is shared across the batch — a 138.11 ms reduction in median latency is a massive improvement for any high-throughput messaging scenario.

**gRPC — Kafka without cloud event (raw payload)**:
- Bulk achieves **200 QPS** vs. **110 QPS** single — **1.8× throughput improvement**
- Latency reduced by **117.21 ms at p50**, **145.92 ms at p90**

Raw payload Kafka publish was already slightly faster than the cloud event variant single-message (110 vs. 91 QPS), but bulk still delivers a meaningful 1.8× boost and eliminates most of the per-message latency penalty.

**gRPC — In-memory with cloud event** (1 KB, 200 QPS):
- Median: **10.79 ms** | p90: **17.19 ms** | p99: **22.28 ms**
- Latency **47.22 ms lower** at p50 vs. single-message publish

Even without the network overhead of an external broker, bulk publish cuts per-message latency significantly for the in-memory case. The gain comes from amortizing the per-call overhead of the Dapr API itself across each batch.

**HTTP — memory broker, batch of 10, 1 KB**:
- **13,635 iterations/sec** bulk throughput | p95: **8.70 ms** — 100% success

**HTTP — memory broker, batch of 100, 1 KB**:
- **2,654 iterations/sec** bulk throughput | p95: **48.06 ms** — 100% success

Larger HTTP batches push more data per call but with higher per-request latency. At batch=10, over 13,000 iterations per second means the system is highly efficient at small batches. At batch=100, the p95 of 48 ms reflects the cost of processing 100 messages per call — the right trade-off for latency-tolerant, high-volume scenarios.

**Kafka pub/subscribe end-to-end (HTTP)**:
- Publish + subscribe round-trip: p50=**3.97 ms**, p95=**8.78 ms** — 100% success

The end-to-end time covers the complete path: publisher sends to Dapr, Dapr delivers to Kafka, Kafka delivers to the subscriber app via Dapr. A 3.97 ms median round-trip for a full Kafka pub/sub cycle is strong — well within real-time application requirements.

---

## HTTP

### TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus

<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="http/TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk

<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal

<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk

<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal

<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" />
<img src="http/TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_variants_duration

<img src="http/TestPubsubBulkPublishHttpPerformance_variants_duration.png" alt="TestPubsubBulkPublishHttpPerformance_variants_duration.png" />

### TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus

<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="http/TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" />

---------------

## gRPC

### TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event

<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)

<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event

<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)

<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" />
<img src="grpc/TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_variants_duration

<img src="grpc/TestBulkPubsubPublishGrpcPerformance_variants_duration.png" alt="TestBulkPubsubPublishGrpcPerformance_variants_duration.png" />
