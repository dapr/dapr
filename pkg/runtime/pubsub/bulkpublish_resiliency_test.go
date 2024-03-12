/*
Copyright 2022 The Dapr Authors
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
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var (
	testLogger = logger.NewLogger("dapr.test")
	zero       = contribPubsub.BulkPublishResponse{}
)

type mockBulkPublisher struct {
	t                 *testing.T
	rwLock            sync.RWMutex
	entryIDRetryTimes map[string]int
	failEvenOnes      bool
	failAllEvents     bool
	applyTimeout      bool
	timeoutSleep      time.Duration
	failCount         int
}

// Pass in failCount to fail the first n times
// Pass in failEvenOnes to fail all events with even entryId
// Pass in failAllEvents to fail all events
func NewMockBulkPublisher(t *testing.T, failCount int, failEvenOnes bool, failAllEvents bool) *mockBulkPublisher {
	return &mockBulkPublisher{
		t:                 t,
		rwLock:            sync.RWMutex{},
		entryIDRetryTimes: map[string]int{},
		failCount:         failCount,
		failEvenOnes:      failEvenOnes,
		failAllEvents:     failAllEvents,
	}
}

func (m *mockBulkPublisher) BulkPublish(ctx context.Context, req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	m.rwLock.Lock()
	defer m.rwLock.Unlock()
	if req == nil {
		return zero, assert.AnError
	}
	if m.applyTimeout {
		time.Sleep(m.timeoutSleep)
		// return some error
		res := contribPubsub.NewBulkPublishResponse(req.Entries, assert.AnError)
		res.FailedEntries = res.FailedEntries[1:]
		return res, assert.AnError
	}
	res := contribPubsub.BulkPublishResponse{
		FailedEntries: make([]contribPubsub.BulkPublishResponseFailedEntry, 0, len(req.Entries)),
	}

	for _, entry := range req.Entries {
		// count the entryId retry times
		if _, ok := m.entryIDRetryTimes[entry.EntryId]; ok {
			m.entryIDRetryTimes[entry.EntryId]++
		} else {
			m.entryIDRetryTimes[entry.EntryId] = 1
		}
		// assert the data and metadata are correct
		assert.Equal(m.t, map[string]string{
			"key" + entry.EntryId: "value" + entry.EntryId,
		}, entry.Metadata)
		assert.Equal(m.t, entry.Event, []byte("data "+entry.EntryId))
	}
	// fail events based on the input count
	if m.failCount > 0 {
		m.failCount--
		for _, entry := range req.Entries {
			k, _ := strconv.ParseInt(entry.EntryId, 10, 32)
			if m.failAllEvents || (k%2 == 0 && m.failEvenOnes) {
				res.FailedEntries = append(res.FailedEntries,
					contribPubsub.BulkPublishResponseFailedEntry{
						EntryId: entry.EntryId,
						Error:   assert.AnError,
					})
			}
		}
		return res, assert.AnError
	}
	return res, nil
}

func TestApplyBulkPublishResiliency(t *testing.T) {
	ctx := context.Background()
	pubsubName := "test-pubsub"
	bulkMessageEntries := []contribPubsub.BulkMessageEntry{
		{
			EntryId: "0",
			Metadata: map[string]string{
				"key0": "value0",
			},
			Event: []byte("data 0"),
		},
		{
			EntryId: "1",
			Metadata: map[string]string{
				"key1": "value1",
			},
			Event: []byte("data 1"),
		},
		{
			EntryId: "2",
			Metadata: map[string]string{
				"key2": "value2",
			},
			Event: []byte("data 2"),
		},
		{
			EntryId: "3",
			Metadata: map[string]string{
				"key3": "value3",
			},
			Event: []byte("data 3"),
		},
		{
			EntryId: "4",
			Metadata: map[string]string{
				"key4": "value4",
			},
			Event: []byte("data 4"),
		},
		{
			EntryId: "5",
			Metadata: map[string]string{
				"key5": "value5",
			},
			Event: []byte("data 5"),
		},
	}
	// Create test retry and timeout configurations
	shortRetry := resiliencyV1alpha.Retry{
		Policy:   "constant",
		Duration: "2s",
	}
	longRetry := resiliencyV1alpha.Retry{
		Policy:   "constant",
		Duration: "10s",
	}
	longTimeout := "10s"
	// Create Mock request
	req := &contribPubsub.BulkPublishRequest{
		Entries:    bulkMessageEntries, // note underling array is shared across tests
		Topic:      "test-topic",
		PubsubName: pubsubName,
	}

	t.Run("fail all events with retries", func(t *testing.T) {
		// Setup
		// fail all events once
		bulkPublisher := NewMockBulkPublisher(t, 1, true, true)

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)

		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		// expecting no final error, the events will pass in the second try
		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		assertRetryCount(t, map[string]int{
			"0": 2,
			"2": 2,
			"4": 2,
			"1": 2,
			"3": 2,
			"5": 2,
		}, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("fail all events exhaust retries", func(t *testing.T) {
		// Setup
		// fail all events and exhaust retries
		// mock bulk publisher set to fail all events 3 times
		bulkPublisher := NewMockBulkPublisher(t, 3, true, true)

		// set short retry with 2 retries max
		shortRetry.MaxRetries = ptr.Of(2)

		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		// Expect final error from the bulk publisher
		require.Error(t, err)
		assert.Equal(t, assert.AnError, err)
		assert.Len(t, res.FailedEntries, 6)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// It is 3 here because the first try is before resiliency kicks in and then it is retried twice
		// which exhausts retries
		assertRetryCount(t, map[string]int{
			"0": 3,
			"2": 3,
			"4": 3,
			"1": 3,
			"3": 3,
			"5": 3,
		}, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("partial failures with retries", func(t *testing.T) {
		// Setup
		// fail events with even Entry ID once, simulate partial failure
		bulkPublisher := NewMockBulkPublisher(t, 1, true, false)

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)

		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		// expecting no final error, all the events will pass in the second try
		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// expecting even Id'ed events alone to be retried
		assertRetryCount(t, map[string]int{
			"0": 2,
			"2": 2,
			"4": 2,
			"1": 1,
			"3": 1,
			"5": 1,
		}, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("no failures", func(t *testing.T) {
		// Setup
		// no failures
		bulkPublisher := NewMockBulkPublisher(t, 0, false, false)

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)

		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		// expecting no final error, all the events will pass in a single try
		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		assertRetryCount(t, map[string]int{
			"0": 1,
			"2": 1,
			"4": 1,
			"1": 1,
			"3": 1,
			"5": 1,
		}, bulkPublisher.entryIDRetryTimes)
	})

	// Partial failures are not possible on timeouts, the whole bulk request will fail
	t.Run("fail all events on timeout", func(t *testing.T) {
		// Setup
		// fail all events due to timeout
		shortTimeout := "1s"
		bulkPublisher := NewMockBulkPublisher(t, 0, false, false)
		bulkPublisher.applyTimeout = true
		bulkPublisher.timeoutSleep = 5 * time.Second

		// set short retry with 0 retry max
		shortRetry.MaxRetries = ptr.Of(0)

		// timeout will be triggered here
		// no retries
		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, shortTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Len(t, res.FailedEntries, 6)
		// not asserting the number of called times since it may or may not be updated(component called) in actually code.
		// In test code, it is not updated.
	})

	t.Run("fail all events with circuitBreaker exhaust retries", func(t *testing.T) {
		// Setup
		// fail all events at least 10 times in a row
		// this will simulate circuitBreaker being triggered
		bulkPublisher := NewMockBulkPublisher(t, 10, true, true)

		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}
		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)
		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act

		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// It is 2 here because the first failure is before resiliency policy starts
		// and after the second failure because the circuitBreaker is configured to trip after a single failure
		// no other requests pass to the bulk publisher.
		expectedCBRetryCount := map[string]int{
			"0": 2,
			"2": 2,
			"4": 2,
			"1": 2,
			"3": 2,
			"5": 2,
		}
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)

		// Act
		// Here the circuitBreaker is open and it will short the request, so the bulkPublisher will not be called
		res, err = ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// Same retry count, bulkPublisher is not called as CB is open
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("partial failures with circuitBreaker and exhaust retries", func(t *testing.T) {
		// Setup
		// fail events with even Ids at least 10 times in a row, simulate partial failures
		// this will also simulate circuitBreaker being triggered
		bulkPublisher := NewMockBulkPublisher(t, 10, true, false)

		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)
		// timeout will not be triggered here

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 3)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// It is 2 here because the first failure is before resiliency policy starts
		// and after the second failure because the circuitBreaker is configured to trip after a single failure
		// no other requests pass to the bulk publisher.
		expectedCBRetryCount := map[string]int{
			"0": 2,
			"2": 2,
			"4": 2,
			"1": 1,
			"3": 1,
			"5": 1,
		}
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)

		// Act
		// Here the circuitBreaker is open and it will short the request, so the bulkPublisher will not be called
		res, err = ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// Same retry count, bulkPublisher is not called as CB is open
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("pass partial failure with CB with short half-open timeout", func(t *testing.T) {
		// Setup
		// fail events with even Ids 2 times in a row, simulate partial failures
		// this will also simulate circuitBreaker being triggered
		bulkPublisher := NewMockBulkPublisher(t, 2, true, false)

		// set a circuit breaker with 1 consecutive failure
		// short half-open timeout to make sure it is triggered
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "1ms",                     // half-open after 1ms. So in test this be triggered
		}

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)
		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// It is 3 here because the first failure is before resiliency policy starts
		// and after the second failure because the circuitBreaker is configured to trip after a single failure
		// Additionally, once more retry is made and circuitBreaker is half-open
		expectedCBRetryCount := map[string]int{
			"0": 3,
			"2": 3,
			"4": 3,
			"1": 1,
			"3": 1,
			"5": 1,
		}
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("pass partial failure with CB exhaust retries then act with short half-open timeout", func(t *testing.T) {
		// Setup
		// fail events with even Ids 2 times in a row, simulate partial failures
		// this will also simulate circuitBreaker being triggered
		bulkPublisher := NewMockBulkPublisher(t, 2, true, false)

		// set a circuit breaker with 1 consecutive failure
		// short half-open timeout to make sure it is triggered
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "4s",                      // half-open after 4s. So in test this be triggered
		}

		// set short retry with 3 retries max
		shortRetry.MaxRetries = ptr.Of(3)
		// timeout will not be triggered here
		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 3)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// It is 2 here because the first failure is before resiliency policy starts
		// and after the second failure because the circuitBreaker is configured to trip after a single failure
		// no other requests pass to the bulk publisher.
		expectedCBRetryCount := map[string]int{
			"0": 2,
			"2": 2,
			"4": 2,
			"1": 1,
			"3": 1,
			"5": 1,
		}
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)

		// Sleep enough time so that CB switches to half-open state
		time.Sleep(5 * time.Second)

		// Act
		// mock bulk publisher will fail the request only twice,
		// the circuitBreaker will be half-open now and then after request served will be closed
		res, err = ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
		assert.Len(t, bulkPublisher.entryIDRetryTimes, 6)
		t.Logf("event ID try count map %v\n\n", bulkPublisher.entryIDRetryTimes)
		// Increase retry count for all event IDs, bulkPublisher is called as CB is half-open
		expectedCBRetryCount = map[string]int{
			"0": 3,
			"2": 3,
			"4": 3,
			"1": 2,
			"3": 2,
			"5": 2,
		}
		assertRetryCount(t, expectedCBRetryCount, bulkPublisher.entryIDRetryTimes)
	})

	t.Run("fail all events with short timeout CB and short retries", func(t *testing.T) {
		// Setup
		// fail events with even Ids at least 10 times in a row, simulate partial failures
		// this will also simulate circuitBreaker being triggered
		// timeout will be triggered here

		bulkPublisher := NewMockBulkPublisher(t, 10, true, false)
		shortTimeout := "1s"
		bulkPublisher.applyTimeout = true
		bulkPublisher.timeoutSleep = 5 * time.Second

		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}
		// set short retry with 2 retries max
		shortRetry.MaxRetries = ptr.Of(2)

		// timeout will be triggered here
		policyProvider := createResPolicyProvider(cb, shortTimeout, shortRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6) // all events fail on timeout
		// not asserting the number of called times since it may or may not be updated(component called) in actually code.
		// In test code, it is not updated.

		// Act
		// Here the circuitBreaker is open and it will short the request, so the bulkPublisher will not be called
		res, err = ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6)
		// Not aaserting the number of called times since it may or may not be updated(component called) in actually code.
		// In above case, it is not updated.
	})

	t.Run("fail events with circuitBreaker, short timeout and long retries", func(t *testing.T) {
		// Setup
		// fail events with even Ids at least 10 times in a row, simulate partial failures
		// this will also simulate circuitBreaker being triggered
		// timeout will be triggered here
		// retries will take longer than the timeout
		// the background goroutines on timeout will complete
		bulkPublisher := NewMockBulkPublisher(t, 10, true, false)
		// short timeout
		shortTimeout := "1s"
		bulkPublisher.applyTimeout = true
		bulkPublisher.timeoutSleep = 5 * time.Second

		// retry time period twice that of timeout sleep and 10 times that of the timeout
		longRetry.MaxRetries = ptr.Of(2)

		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}
		// timeout will be triggered here
		policyProvider := createResPolicyProvider(cb, shortTimeout, longRetry)
		policyDef := policyProvider.ComponentOutboundPolicy(pubsubName, resiliency.Pubsub)

		// Act
		// Make the request twice to make sure circuitBreaker is exhausted
		res, err := ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6) // all events fail on timeout
		// not asserting the number of called times since it may or may not be updated(component called) in actually code.
		// In test code, it is not updated.

		// Act
		// Here the circuitBreaker is open and it will short the request, so the bulkPublisher will not be called
		res, err = ApplyBulkPublishResiliency(ctx, req, policyDef, bulkPublisher)

		// Assert
		require.Error(t, err)
		assert.Equal(t, breaker.ErrOpenState, err)
		assert.Len(t, res.FailedEntries, 6)
		// Not aaserting the number of called times since it may or may not be updated(component called) in actually code.
		// In above case, it is not updated.
	})
}

func createResPolicyProvider(ciruitBreaker resiliencyV1alpha.CircuitBreaker, timeout string, retry resiliencyV1alpha.Retry) *resiliency.Resiliency {
	r := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"pubsubTimeout": timeout,
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"pubsubCircuitBreaker": ciruitBreaker,
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"pubsubRetry": retry,
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"test-pubsub": {
						Outbound: resiliencyV1alpha.PolicyNames{
							Timeout:        "pubsubTimeout",
							CircuitBreaker: "pubsubCircuitBreaker",
							Retry:          "pubsubRetry",
						},
					},
				},
			},
		},
	}
	return resiliency.FromConfigurations(testLogger, r)
}

func assertRetryCount(t *testing.T, expectedIDRetryCountMap map[string]int, actualRetryCountMap map[string]int) {
	for k, v := range expectedIDRetryCountMap {
		assert.Equal(t, v, actualRetryCountMap[k], "expected retry/try count to match")
	}
}
