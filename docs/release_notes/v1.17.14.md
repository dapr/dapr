# Dapr 1.17.14

This update contains the following bug fixes:
- [Pluggable pub/sub components delivered messages to the application one at a time](#pluggable-pubsub-components-delivered-messages-to-the-application-one-at-a-time)

## Pluggable pub/sub components delivered messages to the application one at a time

### Problem

Messages received from a pluggable pub/sub component were dispatched to the application serially.
The receive loop blocked on the handler, which invokes the application, waits for its response and only then acknowledges, so the next message could not be received until the previous one was fully handled.
Each subscription was limited to a single message in flight and its ceiling became the inverse of the per-message latency.
Publishing was unaffected, so a subscriber fell steadily behind a publisher it had previously kept up with, and the queue grew without any error being logged.

Bulk subscribe degraded along with it.
The runtime assembles bulk batches out of concurrent handler calls, so with one caller at a time the configured `maxMessagesCount` could never be reached and every message became its own application request.

### Impact

You were affected if you consumed messages through a pluggable pub/sub component on v1.16.0 or later.
Measured against a RabbitMQ pluggable component publishing 300,000 messages at 5,000 msg/s, consumption fell from 5,004 to 3,026 msg/s, p95 end-to-end latency rose from 15 ms to 37,945 ms, and bulk batches collapsed from a mean of 10.1 messages to exactly 1.
The throughput ceiling tracks handler latency, so the slower the application handler, the more severe the effect: a handler taking 60 ms tops out near 16 msg/s regardless of broker or component capacity.
Built-in pub/sub components were not affected.

### Root Cause

The serialization was collateral damage of a change that set out to stop in-flight input binding events from being cancelled during graceful shutdown.
That change removed the `WaitGroup` `pullMessages` used to wait on in-flight handlers, by then redundant because the runtime tracks in-flight messages itself in `pkg/runtime/subscription`, and collapsed the dispatching goroutine along with it.
Input bindings kept their concurrent dispatch in that same change, and the `safeSend` mutex that exists to make concurrent dispatch safe was left in place here.

### Solution

Messages are dispatched concurrently again, following the shape input bindings already use, so more than one message can be in flight per subscription.
Sends on the stream remain serialized by the existing mutex.
In-flight handlers are now waited on before the stream is closed, so a handler that is still running when the subscription is torn down can still acknowledge the message it has already processed instead of leaving it to be redelivered.
Graceful shutdown semantics are unchanged: draining, pausing and the deadline live in `pkg/runtime/subscription` and count handlers rather than stream goroutines.
