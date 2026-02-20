## Highlights

Dapr Actors in v1.17 demonstrate strong performance across activation, reminder, timer, and high-concurrency stress scenarios. **All test runs completed with 100% success rate and zero pod restarts.**

**How to read these numbers:** Latency percentiles (p50/p90/p99) show the distribution of individual request times — p50 is the typical experience, p99 means only 1 in 100 requests took longer. "VUs" in the stress test are concurrent virtual users each sending independent requests. Reminder registration latency measures the HTTP API response time for scheduling a future callback; reminder trigger throughput measures how fast those callbacks fire once registered.

**Actor activation** (500 QPS, HTTP):
- Median (p50): **2.30 ms** | p90: **2.98 ms** | p99: **6.89 ms**
- 30,000 activations — 100% success

Activating 500 actor instances per second — each requiring the runtime to create and register a new actor — returned a median response in 2.30 ms. The 30,000-activation run completed without a single error, and the tight p50–p90 spread (2.30 ms to 2.98 ms) shows activation latency is highly consistent even under sustained load.

**Actor ID stress test** (high-concurrency):
- **9,443 requests/sec** sustained across **76,000+ requests**
- p50: **20.19 ms** | p90: **61.62 ms** | p95: **76.36 ms** — 100% pass (462 peak VUs)

This test targeted many distinct actor IDs simultaneously with up to 462 concurrent virtual users, stressing Dapr's actor placement and routing subsystem. Sustaining 9,443 requests/sec across 76,000+ requests with a 100% pass rate demonstrates robust actor routing under heavy concurrent load. The higher p90–p95 latencies (61–76 ms) reflect natural queuing as the placement service routes requests across many actors.

**Reminder registration** (HTTP):
- 667 QPS sustained, 40,039 reminder registrations — **100% success** (all HTTP 204)
- p50: **35.99 ms** | p90: **55.86 ms** | p99: **74.47 ms**

Each reminder registration requires the scheduler to persist and schedule a future callback. Registering over 40,000 reminders at 667 QPS with no failures shows the scheduler subsystem handles high registration volume reliably. The 35.99 ms median reflects the cost of durable scheduling — the reminder is persisted before the API returns, so this latency is the price of reliability.

**Reminder trigger throughput**:
- **15,463 reminders triggered per second** across 100,000 total triggers

Once registered, reminders fired at 15,463 per second — demonstrating that reminder dispatch at scale is extremely fast once the scheduling work is done upfront. This separation between registration latency (tens of ms) and trigger throughput (thousands per second) is by design.

**Timer with state** (220 QPS, HTTP):
- Median (p50): **5.79 ms** | p90: **11.35 ms** | p99: **18.76 ms**
- 13,200 requests — 100% success

Actor timers with state combine two operations per request: triggering the timer callback and reading/writing actor state. The 5.79 ms median reflects this composite operation at 220 QPS — both timer dispatch and state access completing in a single round-trip with consistent tail behavior.

---

### TestActorActivate

<img src="TestActorActivate_data_volume.png" alt="TestActorActivate_data_volume.png" />
<img src="TestActorActivate_duration_breakdown.png" alt="TestActorActivate_duration_breakdown.png" />
<img src="TestActorActivate_duration_requested_vs_actual.png" alt="TestActorActivate_duration_requested_vs_actual.png" />
<img src="TestActorActivate_header_size.png" alt="TestActorActivate_header_size.png" />
<img src="TestActorActivate_histogram_count.png" alt="TestActorActivate_histogram_count.png" />
<img src="TestActorActivate_histogram_percent.png" alt="TestActorActivate_histogram_percent.png" />
<img src="TestActorActivate_payload_size.png" alt="TestActorActivate_payload_size.png" />
<img src="TestActorActivate_qps.png" alt="TestActorActivate_qps.png" />
<img src="TestActorActivate_resource_cpu.png" alt="TestActorActivate_resource_cpu.png" />
<img src="TestActorActivate_resource_memory.png" alt="TestActorActivate_resource_memory.png" />
<img src="TestActorActivate_summary.png" alt="TestActorActivate_summary.png" />
<img src="TestActorActivate_tail_latency.png" alt="TestActorActivate_tail_latency.png" />
<img src="TestActorActivate_throughput.png" alt="TestActorActivate_throughput.png" />

### TestActorDoubleActivation

<img src="TestActorDoubleActivation_data_volume.png" alt="TestActorDoubleActivation_data_volume.png" />
<img src="TestActorDoubleActivation_duration_breakdown.png" alt="TestActorDoubleActivation_duration_breakdown.png" />
<img src="TestActorDoubleActivation_duration_low.png" alt="TestActorDoubleActivation_duration_low.png" />
<img src="TestActorDoubleActivation_resource_cpu.png" alt="TestActorDoubleActivation_resource_cpu.png" />
<img src="TestActorDoubleActivation_resource_memory.png" alt="TestActorDoubleActivation_resource_memory.png" />
<img src="TestActorDoubleActivation_summary.png" alt="TestActorDoubleActivation_summary.png" />
<img src="TestActorDoubleActivation_tail_latency.png" alt="TestActorDoubleActivation_tail_latency.png" />
<img src="TestActorDoubleActivation_throughput.png" alt="TestActorDoubleActivation_throughput.png" />

### TestActorIdStress

<img src="TestActorIdStress_data_volume.png" alt="TestActorIdStress_data_volume.png" />
<img src="TestActorIdStress_duration_breakdown.png" alt="TestActorIdStress_duration_breakdown.png" />
<img src="TestActorIdStress_duration_low.png" alt="TestActorIdStress_duration_low.png" />
<img src="TestActorIdStress_resource_cpu.png" alt="TestActorIdStress_resource_cpu.png" />
<img src="TestActorIdStress_resource_memory.png" alt="TestActorIdStress_resource_memory.png" />
<img src="TestActorIdStress_summary.png" alt="TestActorIdStress_summary.png" />
<img src="TestActorIdStress_tail_latency.png" alt="TestActorIdStress_tail_latency.png" />
<img src="TestActorIdStress_throughput.png" alt="TestActorIdStress_throughput.png" />

### TestActorReminderRegistrationPerformance

<img src="TestActorReminderRegistrationPerformance_connection_stats.png" alt="TestActorReminderRegistrationPerformance_connection_stats.png" />
<img src="TestActorReminderRegistrationPerformance_data_volume.png" alt="TestActorReminderRegistrationPerformance_data_volume.png" />
<img src="TestActorReminderRegistrationPerformance_duration_breakdown.png" alt="TestActorReminderRegistrationPerformance_duration_breakdown.png" />
<img src="TestActorReminderRegistrationPerformance_duration_requested_vs_actual.png" alt="TestActorReminderRegistrationPerformance_duration_requested_vs_actual.png" />
<img src="TestActorReminderRegistrationPerformance_header_size.png" alt="TestActorReminderRegistrationPerformance_header_size.png" />
<img src="TestActorReminderRegistrationPerformance_histogram_count.png" alt="TestActorReminderRegistrationPerformance_histogram_count.png" />
<img src="TestActorReminderRegistrationPerformance_histogram_percent.png" alt="TestActorReminderRegistrationPerformance_histogram_percent.png" />
<img src="TestActorReminderRegistrationPerformance_payload_size.png" alt="TestActorReminderRegistrationPerformance_payload_size.png" />
<img src="TestActorReminderRegistrationPerformance_qps.png" alt="TestActorReminderRegistrationPerformance_qps.png" />
<img src="TestActorReminderRegistrationPerformance_resource_cpu.png" alt="TestActorReminderRegistrationPerformance_resource_cpu.png" />
<img src="TestActorReminderRegistrationPerformance_resource_memory.png" alt="TestActorReminderRegistrationPerformance_resource_memory.png" />
<img src="TestActorReminderRegistrationPerformance_summary.png" alt="TestActorReminderRegistrationPerformance_summary.png" />
<img src="TestActorReminderRegistrationPerformance_tail_latency.png" alt="TestActorReminderRegistrationPerformance_tail_latency.png" />
<img src="TestActorReminderRegistrationPerformance_throughput.png" alt="TestActorReminderRegistrationPerformance_throughput.png" />

### TestActorTimerWithStatePerformance

<img src="TestActorTimerWithStatePerformance_connection_stats.png" alt="TestActorTimerWithStatePerformance_connection_stats.png" />
<img src="TestActorTimerWithStatePerformance_data_volume.png" alt="TestActorTimerWithStatePerformance_data_volume.png" />
<img src="TestActorTimerWithStatePerformance_duration_breakdown.png" alt="TestActorTimerWithStatePerformance_duration_breakdown.png" />
<img src="TestActorTimerWithStatePerformance_duration_requested_vs_actual.png" alt="TestActorTimerWithStatePerformance_duration_requested_vs_actual.png" />
<img src="TestActorTimerWithStatePerformance_header_size.png" alt="TestActorTimerWithStatePerformance_header_size.png" />
<img src="TestActorTimerWithStatePerformance_histogram_count.png" alt="TestActorTimerWithStatePerformance_histogram_count.png" />
<img src="TestActorTimerWithStatePerformance_histogram_percent.png" alt="TestActorTimerWithStatePerformance_histogram_percent.png" />
<img src="TestActorTimerWithStatePerformance_payload_size.png" alt="TestActorTimerWithStatePerformance_payload_size.png" />
<img src="TestActorTimerWithStatePerformance_qps.png" alt="TestActorTimerWithStatePerformance_qps.png" />
<img src="TestActorTimerWithStatePerformance_resource_cpu.png" alt="TestActorTimerWithStatePerformance_resource_cpu.png" />
<img src="TestActorTimerWithStatePerformance_resource_memory.png" alt="TestActorTimerWithStatePerformance_resource_memory.png" />
<img src="TestActorTimerWithStatePerformance_summary.png" alt="TestActorTimerWithStatePerformance_summary.png" />
<img src="TestActorTimerWithStatePerformance_tail_latency.png" alt="TestActorTimerWithStatePerformance_tail_latency.png" />
<img src="TestActorTimerWithStatePerformance_throughput.png" alt="TestActorTimerWithStatePerformance_throughput.png" />
