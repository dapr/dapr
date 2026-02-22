# Dapr v1.17 Performance Highlights

Performance results from the v1.17 release test suite. All tests ran on Kubernetes with zero pod restarts and 100% success rates unless noted. For full charts per API, see the subdirectories linked in each section.

## How to read these numbers

**Latency percentiles** — each test fires thousands of requests and records how long each one took. Results are reported as percentiles:
- **p50 (median)**: Half of all requests completed faster than this — the typical user experience.
- **p90**: 9 out of 10 requests completed faster than this — what most users see under sustained load.
- **p99**: Only 1 in 100 requests took longer — captures tail latency, important for SLA planning.
- **p99.9**: Only 1 in 1,000 requests exceeded this — the extreme edge of the distribution.

**Connections** — the number of persistent parallel connections the load generator held open to the Dapr sidecar. Dapr sits between the calling service and the target app, so "16 connections" means 16 concurrent callers hitting the Dapr sidecar simultaneously. The sidecar then forwards each request to the target service.

**QPS (queries/requests per second)** — the request rate the load generator sustained. Actual QPS values were within 0.1% of the target in every test.

**Dapr overhead** — measured by running the identical test directly between services (bypassing Dapr) and subtracting that baseline latency from the Dapr-proxied result. It represents the pure cost of the Dapr sidecar proxy layer.

**VUs (virtual users)** — used in workflow and scenario-based tests; the number of concurrent users or workflow instances active simultaneously.

**Pod restarts** — if an app or sidecar crashed or was OOM-killed during a test, Kubernetes restarts it. Zero restarts across all tests means the system stayed stable under every load scenario tested.

---

## Service Invocation → [charts](./service_invocation/)

Service invocation is Dapr's core inter-service communication primitive. In v1.17, it delivers sub-2ms median latency over HTTP and sub-3ms over gRPC at 1,000 QPS sustained load — with zero failures across 60,000 requests per run.

**HTTP** (1 KB payload, 16 connections, 1,000 QPS target):
- Median (p50): **1.59 ms** | p90: **2.38 ms** | p99: **3.89 ms**
- Dapr adds **1.06 ms** at median vs. direct invocation
- 60,000 requests — **100% success rate**, 0 errors, 0 pod restarts

With 16 parallel connections sustaining 1,000 requests per second, the typical HTTP round-trip through Dapr was 1.59 ms end-to-end. Even the worst 1 in 100 requests (p99) stayed under 4 ms. Dapr's sidecar accounted for just 1.06 ms of that total — the rest is network and application time.

**gRPC** (1 KB payload, 16 connections, 1,000 QPS target):
- Median (p50): **2.25 ms** | p90: **2.92 ms** | p99: **4.59 ms**
- Dapr adds **1.68 ms** at median vs. direct invocation
- 60,000 requests — **100% success rate**, 0 errors, 0 pod restarts

The gRPC path adds slightly more sidecar overhead than HTTP (1.68 ms vs. 1.06 ms at median) because Dapr must participate in HTTP/2 framing on both sides of the connection. Even so, median latency stays under 2.5 ms at 1,000 QPS with a tight p90–p99 spread, indicating very consistent tail behavior.

Both transports hit their QPS targets with near-perfect accuracy (999.94–999.95 actual vs. 1,000 requested). The sidecar consumes under 250 mCPU and ~51 MB memory under this load.

---

## Workflows → [charts](./workflows/)

Dapr Workflows in v1.17 demonstrate reliable execution across a wide range of concurrency levels and workflow patterns, with **100% success rate across all test configurations** and zero pod restarts.

**Parallel workflow execution** (110 VUs — 110 concurrent workflow instances):
- 440 workflows completed at **97.91 workflows/sec**
- Median duration: **1.06 s** | p90: **1.27 s** | p95: **1.31 s**

With 110 workflows running simultaneously, each fanning out parallel activities, the typical end-to-end completion was 1.06 s. The tight p90–p95 spread (1.27 s to 1.31 s) shows that concurrency does not introduce meaningful tail latency — Dapr handles the parallel fan-out consistently.

**Series workflow execution** (350 VUs — 350 concurrent sequential workflows):
- 1,400 workflows completed at **57.43 workflows/sec**
- Median: **6.06 s** | p90: **6.19 s** | p95: **6.22 s**

Series workflows execute activities one after another, so each step adds to total duration. The extremely narrow p90–p95 band (6.19 s to 6.22 s) means execution time is highly predictable regardless of concurrency — 350 workflows running simultaneously produce nearly identical individual runtimes.

**Constant VU throughput** (30 VUs, averaged across 3 independent runs):
- ~300 workflows at **~28 workflows/sec**
- Median: **~1.04 s** | p95: **~1.18 s**

Three independent runs at 30 VUs produced nearly identical results, demonstrating consistent throughput with no degradation across repeated test iterations.

**Scale test** (500 VUs — 500 concurrent delay-based workflows):
- **10,000 workflows** completed at **35.45 workflows/sec**
- Median: **11.11 s** | p90: **19.97 s** | p95: **29.94 s**

At 500 simultaneous workflow instances, Dapr sustained 35.45 completions per second across 10,000 total runs. The wider p90–p95 spread at this scale reflects natural queuing — individual workflows may wait briefly before being scheduled — while still completing without a single failure.

**Multi-payload workflows** (30 VUs, 3 different payload sizes, averaged across 3 runs):
- ~300 workflows at **~8.92 workflows/sec** per run
- Median: **~3.27 s** | p95: **~3.67 s**

Across three different payload sizes, median and p95 latencies remained stable, confirming that varying message sizes do not meaningfully impact workflow execution time.

---

## State → [charts](./state/)

State management in v1.17 delivers sub-millisecond median latency on both transports at 1,000 QPS sustained load — among the lowest overhead of any Dapr API surface.

**gRPC** (16 connections, 1,000 QPS target):
- Median (p50): **0.57 ms** | p90: **0.93 ms** | p99: **1.65 ms** | p99.9: **2.66 ms**
- Dapr adds just **+0.12 ms** at p50 vs. direct store access
- 60,000 requests — **100% success rate**, 0 pod restarts

Sub-millisecond median at 1,000 QPS means state reads through Dapr are effectively instant in any real-world context. The sidecar added just 0.12 ms on top of going directly to the state store — Dapr's proxy overhead here is negligible. Even at the 99.9th percentile (the worst 1 in 1,000 requests), latency stays under 3 ms.

**HTTP** (16 connections, 1,000 QPS target):
- Median (p50): **0.73 ms** | p90: **1.60 ms** | p99: **1.97 ms** | p99.9: **2.84 ms**
- Dapr adds just **+0.28 ms** at p50 vs. direct store access
- 60,000 requests — **100% success rate**, 0 pod restarts

HTTP state access is similarly fast with a median under 1 ms. The slightly higher overhead (0.28 ms vs. 0.12 ms for gRPC) reflects the additional work of HTTP/1.1 header parsing compared to gRPC's binary framing — both remain well within sub-millisecond territory for the typical request.

Both transports hit their QPS targets precisely (999.97–999.98 actual vs. 1,000 requested).

---

## PubSub → [charts](./pubsub/)

Dapr pubsub in v1.17 delivers sub-millisecond publish latency at 1,000 QPS on gRPC, with the Bulk Publish API providing dramatic throughput improvements for high-volume scenarios. All test runs completed with **100% success rate** and zero pod restarts.

**Standard publish — gRPC** (16 connections, 1,000 QPS):
- Median (p50): **0.60 ms** | p90: **0.94 ms** | p99: **1.87 ms**
- 60,000 publish operations — 100% success

With 60,000 publish operations sustaining 1,000 QPS, the Dapr sidecar received each message from the publisher and forwarded it to the message broker in a median of 0.60 ms — well under 1 ms, with 9 out of 10 requests completing in under 1 ms as well.

**Standard publish — HTTP**:
- Median (p50): **0.35 ms** | p95: **0.49 ms** — 100% success

HTTP publish latency is even lower in this test configuration, with the majority of requests completing in 0.35 ms and 95% completing in under 0.5 ms.

**Bulk Publish API — Kafka (gRPC)** — the most impactful scenario for high-throughput systems:
- Single-message publish: **91 QPS** → Bulk publish: **200 QPS** — **2.2× throughput improvement**
- Latency reduced by **138.11 ms at p50** and **223.50 ms at p90** vs. single-message publishing

Without bulk publish, Kafka's per-message broker overhead capped throughput at ~91 QPS. The Bulk Publish API batches multiple messages into a single broker write, unlocking the full 200 QPS target. This isn't just a throughput win — p50 latency dropped by 138.11 ms per message, meaning each individual message also arrives significantly faster when sent in bulk.

**Bulk Publish API — In-memory (gRPC)** (cloud event, 1 KB payload):
- Median: **10.79 ms** | p90: **17.19 ms** — with latency up to **47.22 ms lower** than single-message

Even for in-memory brokers (no network hop to an external broker), bulk publish reduces per-message latency by 47.22 ms at the median compared to publishing messages one at a time.

**Bulk Publish HTTP** (memory broker, batch of 10, 1 KB):
- **13,635 iterations/sec** throughput, p95: **8.70 ms** — 100% success

HTTP bulk publish at batch=10 achieves over 13,000 requests per second — 3.7× the throughput of single-message HTTP publish — with 95% of requests completing in under 9 ms.

---

## Actors → [charts](./actors/)

Dapr Actors in v1.17 demonstrate strong performance across activation, reminder, timer, and high-concurrency stress scenarios. **All test runs completed with 100% success rate and zero pod restarts.**

**Actor activation** (500 QPS, HTTP):
- Median (p50): **2.30 ms** | p90: **2.98 ms** | p99: **6.89 ms**
- 30,000 activations — 100% success

Activating 500 actor instances per second — each requiring the runtime to create and register a new actor — returned a median response in 2.30 ms. The 30,000-activation run completed without a single error, and the tight p50–p90 spread (2.30 ms to 2.98 ms) shows activation latency is highly consistent.

**Actor ID stress test** (high-concurrency):
- **9,443 requests/sec** sustained across **76,000+ requests**
- p50: **20.19 ms** | p90: **61.62 ms** | p95: **76.36 ms** — 100% pass (462 peak VUs)

This test targeted many distinct actor IDs simultaneously with up to 462 concurrent virtual users, stressing Dapr's actor placement and routing subsystem. Sustaining 9,443 requests/sec across 76,000+ requests with a 100% pass rate demonstrates robust actor routing under heavy concurrent load.

**Reminder registration** (HTTP):
- 667 QPS sustained, 40,039 reminder registrations — **100% success** (all HTTP 204)
- p50: **35.99 ms** | p90: **55.86 ms** | p99: **74.47 ms**

Each reminder registration requires the scheduler to persist and schedule a future callback. Registering over 40,000 reminders at 667 QPS with no failures shows the scheduler subsystem handles high registration volume reliably.

**Reminder trigger throughput**:
- **15,463 reminders triggered per second** across 100,000 total triggers

Once registered, reminders fired at 15,463 per second — demonstrating that reminder dispatch at scale is extremely fast once the scheduling work is done upfront.

**Timer with state** (220 QPS, HTTP):
- Median (p50): **5.79 ms** | p90: **11.35 ms** | p99: **18.76 ms**
- 13,200 requests — 100% success

Actor timers with state combine two operations per request: triggering the timer callback and reading/writing actor state. The 5.79 ms median reflects this composite operation at 220 QPS with consistent tail behavior.

---

## Configuration → [charts](./configuration/)

Configuration API in v1.17 handles high throughput for Get operations and adds minimal overhead for Subscribe. All test runs completed with **100% success rate** and zero pod restarts.

**Configuration Get — gRPC**:
- **26,810 iterations/sec** throughput | p50: **6.67 ms** | p90: **16.35 ms** | p95: **21.53 ms**
- 241,601 total requests — 100% success

The configuration Get API handled over 26,000 requests per second via gRPC, with the typical request completing in 6.67 ms. Across 241,601 total requests, every single one succeeded.

**Configuration Get — HTTP**:
- **23,326 iterations/sec** throughput | p50: **8.27 ms** | p90: **18.29 ms** | p95: **23.14 ms**
- 210,368 total requests — 100% success

HTTP configuration Get is similarly fast, handling 23,326 requests per second with a median of 8.27 ms and p95 under 24 ms.

**Configuration Subscribe — gRPC**:
- **968 iterations/sec** | p95: **510.58 ms** — near-zero Dapr overhead vs. baseline (**+0.36%**)

Subscribe is event-driven: the test measures how long a subscribed client waits to receive a configuration change notification. The ~500 ms p95 is driven by the polling interval of the config store, not Dapr — Dapr itself added only +1.82 ms (+0.36%) over going directly to the store, which is effectively zero overhead for a subscription workload.

**Configuration Subscribe — HTTP**:
- **981 iterations/sec** | p95: **473.20 ms** — 100% success

HTTP subscribe shows similar notification delivery characteristics, with 981 subscription checks per second and consistent delivery latency.
