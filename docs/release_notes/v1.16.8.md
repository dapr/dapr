# Dapr 1.16.8

This update includes bug fixes:

- [RabbitMQ PubSub concurrent map writes panic](#rabbitmq-pubsub-concurrent-map-writes-panic)

## RabbitMQ PubSub concurrent map writes panic

### Problem

After upgrading to Dapr v1.16, applications using RabbitMQ PubSub started experiencing panics with the message "fatal error: concurrent map writes". The error occurred during subscription management in the RabbitMQ PubSub component.

### Impact

The Dapr sidecar would panic and crash when using RabbitMQ PubSub components, potentially causing service disruption.

### Root Cause

The subscription code was incorrectly using read locks (`RLock`/`RUnlock`) when writing to a shared map, causing concurrent map writes and panics.

### Solution

Fixed the mutex usage by changing from read locks to write locks (`Lock`/`Unlock`) in the subscription management code to ensure thread-safe access to shared data.
