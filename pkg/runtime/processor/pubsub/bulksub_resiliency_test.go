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

package pubsub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	pubsubName = "pubsubName"
)

var testLogger = logger.NewLogger("dapr.runtime.test")

type input struct {
	pbsm     bulkSubscribedMessage
	bscData  bulkSubscribeCallData
	envelope map[string]interface{}
}

type testSettings struct {
	entryIdRetryTimes map[string]int //nolint:stylecheck
	failEvenOnes      bool
	failAllEntries    bool
	failCount         int
}

func getBulkMessageEntriesForResiliency(len int) []contribpubsub.BulkMessageEntry {
	bulkEntries := make([]contribpubsub.BulkMessageEntry, 10)

	bulkEntries[0] = contribpubsub.BulkMessageEntry{EntryId: "1111111a", Event: []byte(order1)}
	bulkEntries[1] = contribpubsub.BulkMessageEntry{EntryId: "2222222b", Event: []byte(order2)}
	bulkEntries[2] = contribpubsub.BulkMessageEntry{EntryId: "333333c", Event: []byte(order3)}
	bulkEntries[3] = contribpubsub.BulkMessageEntry{EntryId: "4444444d", Event: []byte(order4)}
	bulkEntries[4] = contribpubsub.BulkMessageEntry{EntryId: "5555555e", Event: []byte(order5)}
	bulkEntries[5] = contribpubsub.BulkMessageEntry{EntryId: "66666666f", Event: []byte(order6)}
	bulkEntries[6] = contribpubsub.BulkMessageEntry{EntryId: "7777777g", Event: []byte(order7)}
	bulkEntries[7] = contribpubsub.BulkMessageEntry{EntryId: "8888888h", Event: []byte(order8)}
	bulkEntries[8] = contribpubsub.BulkMessageEntry{EntryId: "9999999i", Event: []byte(order9)}
	bulkEntries[9] = contribpubsub.BulkMessageEntry{EntryId: "10101010j", Event: []byte(order10)}

	return bulkEntries[:len]
}

var shortRetry = resiliencyV1alpha.Retry{
	Policy:   "constant",
	Duration: "1s",
}

var longRetry = resiliencyV1alpha.Retry{
	Policy:   "constant",
	Duration: "5s",
}

var (
	longTimeout  = "10s"
	shortTimeout = "1s"
)

var orders []string = []string{order1, order2, order3, order4, order5, order6, order7, order8, order9, order10}

func getPubSubMessages() []message {
	pubSubMessages := make([]message, 10)

	bulkEntries := getBulkMessageEntriesForResiliency(10)
	i := 0
	for _, ord := range orders {
		var cloudEvent map[string]interface{}
		err := json.Unmarshal([]byte(ord), &cloudEvent)
		if err == nil {
			pubSubMessages[i].cloudEvent = cloudEvent
		}
		pubSubMessages[i].entry = &bulkEntries[i]
		rawData := runtimePubsub.BulkSubscribeMessageItem{
			EntryId: bulkEntries[i].EntryId,
			Event:   cloudEvent,
		}
		pubSubMessages[i].rawData = &rawData
		i++
	}
	return pubSubMessages
}

func createResPolicyProvider(ciruitBreaker resiliencyV1alpha.CircuitBreaker, timeout string,
	retry resiliencyV1alpha.Retry,
) *resiliency.Resiliency {
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
					"pubsubName": {
						Inbound: resiliencyV1alpha.PolicyNames{
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

func getResponse(req *invokev1.InvokeMethodRequest, ts *testSettings) *invokev1.InvokeMethodResponse {
	var data map[string]any
	v, _ := req.RawDataFull()
	e := json.Unmarshal(v, &data)
	appResponses := []contribpubsub.AppBulkResponseEntry{}
	if e == nil {
		entries, _ := data["entries"].([]any)
		for j := 1; j <= len(entries); j++ {
			entryId, _ := entries[j-1].(map[string]any)["entryId"].(string) //nolint:stylecheck
			abre := contribpubsub.AppBulkResponseEntry{
				EntryId: entryId,
			}
			if ts.failCount > 0 && (ts.failAllEntries || (ts.failEvenOnes && j%2 == 0)) {
				testLogger.Infof("ts.failCount: %d", ts.failCount)
				abre.Status = "RETRY"
			} else {
				abre.Status = "SUCCESS"
			}
			appResponses = append(appResponses, abre)

			if _, ok := ts.entryIdRetryTimes[entryId]; ok {
				ts.entryIdRetryTimes[entryId]++
			} else {
				ts.entryIdRetryTimes[entryId] = 1
			}
		}
		ts.failCount--
	}
	re := contribpubsub.AppBulkResponse{
		AppResponses: appResponses,
	}
	v, _ = json.Marshal(re)
	respInvoke := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataBytes(v).
		WithContentType("application/json")
	return respInvoke
}

func getInput() input {
	in := input{}
	testBulkSubscribePubsub := "bulkSubscribePubSub"
	msgArr := getBulkMessageEntriesForResiliency(10)
	psMessages := getPubSubMessages()
	in.pbsm = bulkSubscribedMessage{
		pubSubMessages: psMessages,
		topic:          "topic0",
		pubsub:         testBulkSubscribePubsub,
		path:           orders1,
		length:         len(psMessages),
	}

	bulkResponses := make([]contribpubsub.BulkSubscribeResponseEntry, 10)
	in.bscData.bulkResponses = &bulkResponses
	entryIdIndexMap := make(map[string]int) //nolint:stylecheck
	in.bscData.entryIdIndexMap = &entryIdIndexMap
	for i, entry := range msgArr {
		(*in.bscData.entryIdIndexMap)[entry.EntryId] = i
	}
	in.envelope = runtimePubsub.NewBulkSubscribeEnvelope(&runtimePubsub.BulkSubscribeEnvelope{
		ID:     "",
		Topic:  "topic0",
		Pubsub: testBulkSubscribePubsub,
	})
	bulkSubDiag := newBulkSubIngressDiagnostics()
	in.bscData.bulkSubDiag = &bulkSubDiag
	in.bscData.topic = "topic0"
	in.bscData.psName = testBulkSubscribePubsub
	return in
}

func TestBulkSubscribeResiliency(t *testing.T) {
	t.Run("verify Responses when few entries fail even after retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps := New(Options{
			Registry:   reg.PubSubs(),
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
		})
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         4,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		assertRetryCount(t, map[string]int{
			"1111111a":  1,
			"2222222b":  2,
			"333333c":   1,
			"4444444d":  3,
			"5555555e":  1,
			"66666666f": 2,
			"7777777g":  1,
			"8888888h":  3,
			"9999999i":  1,
			"10101010j": 2,
		}, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when ALL entries fail even after retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         4,
			failEvenOnes:      true,
			failAllEntries:    true,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: true},
				{EntryId: "10101010j", IsError: true},
			},
		}
		assertRetryCount(t, map[string]int{
			"1111111a":  3,
			"2222222b":  3,
			"333333c":   3,
			"4444444d":  3,
			"5555555e":  3,
			"66666666f": 3,
			"7777777g":  3,
			"8888888h":  3,
			"9999999i":  3,
			"10101010j": 3,
		}, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("pass ALL entries in second attempt", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         1,
			failEvenOnes:      false,
			failAllEntries:    true,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		assertRetryCount(t, map[string]int{
			"1111111a":  2,
			"2222222b":  2,
			"333333c":   2,
			"4444444d":  2,
			"5555555e":  2,
			"66666666f": 2,
			"7777777g":  2,
			"8888888h":  2,
			"9999999i":  2,
			"10101010j": 2,
		}, ts.entryIdRetryTimes)
		require.NoError(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("pass ALL entries in first attempt", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         0,
			failEvenOnes:      false,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
		assert.Len(t, *b, 10)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		assertRetryCount(t, map[string]int{
			"1111111a":  1,
			"2222222b":  1,
			"333333c":   1,
			"4444444d":  1,
			"5555555e":  1,
			"66666666f": 1,
			"7777777g":  1,
			"8888888h":  1,
			"9999999i":  1,
			"10101010j": 1,
		}, ts.entryIdRetryTimes)
		require.NoError(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("fail ALL entries due to timeout", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         0,
			failEvenOnes:      false,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.
			On(
				"InvokeMethod",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
					return req.Message().GetMethod() == orders1
				}),
			).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, shortTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		assert.Len(t, *b, 10)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: true},
				{EntryId: "10101010j", IsError: true},
			},
		}
		require.Error(t, e)
		require.ErrorIs(t, e, context.DeadlineExceeded)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when ALL entries fail with Circuitbreaker and exhaust retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    true,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}

		shortRetry.MaxRetries = ptr.Of(3)

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: true},
				{EntryId: "10101010j", IsError: true},
			},
		}
		expectedCBRetryCount := map[string]int{
			"1111111a":  2,
			"2222222b":  2,
			"333333c":   2,
			"4444444d":  2,
			"5555555e":  2,
			"66666666f": 2,
			"7777777g":  2,
			"8888888h":  2,
			"9999999i":  2,
			"10101010j": 2,
		}
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when Partial entries fail with Circuitbreaker and exhaust retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will not be triggered
		}

		shortRetry.MaxRetries = ptr.Of(5)

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount := map[string]int{
			"1111111a":  1,
			"2222222b":  2,
			"333333c":   1,
			"4444444d":  2,
			"5555555e":  1,
			"66666666f": 2,
			"7777777g":  1,
			"8888888h":  2,
			"9999999i":  1,
			"10101010j": 2,
		}
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when Partial entries Pass with Circuitbreaker half open timeout", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         2,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "1ms",                     // half-open after 1ms. So in test this will be triggered
		}

		shortRetry.MaxRetries = ptr.Of(3)

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount := map[string]int{
			"1111111a":  1,
			"2222222b":  2,
			"333333c":   1,
			"4444444d":  3,
			"5555555e":  1,
			"66666666f": 2,
			"7777777g":  1,
			"8888888h":  3,
			"9999999i":  1,
			"10101010j": 2,
		}
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.NoError(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("Partial success with CB and exhaust retries, then act with short half open timeout", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         2,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "4s",                      // half-open after 4s. So in test this will be triggered
		}

		shortRetry.MaxRetries = ptr.Of(3)

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount := map[string]int{
			"1111111a":  1,
			"2222222b":  2,
			"333333c":   1,
			"4444444d":  2,
			"5555555e":  1,
			"66666666f": 2,
			"7777777g":  1,
			"8888888h":  2,
			"9999999i":  1,
			"10101010j": 2,
		}
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)

		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse = BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount = map[string]int{
			"1111111a":  2,
			"2222222b":  3,
			"333333c":   2,
			"4444444d":  3,
			"5555555e":  2,
			"66666666f": 3,
			"7777777g":  2,
			"8888888h":  3,
			"9999999i":  2,
			"10101010j": 3,
		}
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.NoError(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("Fail all events with timeout and then Open CB - short retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.
			On(
				"InvokeMethod",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
					return req.Message().GetMethod() == orders1
				}),
			).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will NOT be triggered
		}

		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(cb, shortTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.Background(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: true},
				{EntryId: "10101010j", IsError: true},
			},
		}
		assert.Len(t, *b, 10)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		assert.Len(t, *b, 10)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})
}

func TestBulkSubscribeResiliencyStateConversionsFromHalfOpen(t *testing.T) {
	t.Run("verify Responses when Circuitbreaker half open state changes happen", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         3,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
				return req.Message().GetMethod() == orders1
			}),
		)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "4s",                      // half-open after 4s. So in test this will be triggered
		}

		shortRetry.MaxRetries = ptr.Of(20)

		policyProvider := createResPolicyProvider(cb, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()

		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount := map[string]int{
			"1111111a":  1,
			"2222222b":  2,
			"333333c":   1,
			"4444444d":  2,
			"5555555e":  1,
			"66666666f": 2,
			"7777777g":  1,
			"8888888h":  2,
			"9999999i":  1,
			"10101010j": 2,
		}
		// 2 invoke calls should be made here, as the circuit breaker becomes open
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)
		// after this time, circuit breaker should be half-open
		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse = BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: true},
			},
		}
		expectedCBRetryCount = map[string]int{
			"1111111a":  2,
			"2222222b":  3,
			"333333c":   2,
			"4444444d":  3,
			"5555555e":  2,
			"66666666f": 3,
			"7777777g":  2,
			"8888888h":  3,
			"9999999i":  2,
			"10101010j": 3,
		}
		// as this operation is partial failure case and circuit breaker is half-open, this failure
		// would mark state as open
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		// circuit breaker is open, so no call should go through
		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)
		// after this time, circuit breaker should be half-open
		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse = BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: false},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		expectedCBRetryCount = map[string]int{
			"1111111a":  3,
			"2222222b":  4,
			"333333c":   3,
			"4444444d":  4,
			"5555555e":  3,
			"66666666f": 4,
			"7777777g":  3,
			"8888888h":  4,
			"9999999i":  3,
			"10101010j": 4,
		}
		// As this operation succeeds with all entries passed, circuit breaker should be closed
		// as successCount becomes equal or greater than maxRequests
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 4)
		assert.Len(t, *b, 10)
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		require.NoError(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})
}

func TestBulkSubscribeResiliencyWithLongRetries(t *testing.T) {
	t.Run("Fail all events with timeout and then Open CB - long retries", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry: reg.PubSubs(),
			IsHTTP:   true,
		})
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		mockee := mockAppChannel.
			On(
				"InvokeMethod",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
					return req.Message().GetMethod() == orders1
				}),
			).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 := getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		// set a circuit breaker with 1 consecutive failure
		cb := resiliencyV1alpha.CircuitBreaker{
			Trip:        "consecutiveFailures > 1", // circuitBreaker will open after 1 failure, after the retries
			MaxRequests: 1,                         // only 1 request will be allowed when circuitBreaker is half-open
			Timeout:     "30s",                     // half-open after 30s. So in test this will NOT be triggered
		}

		shortRetry.MaxRetries = ptr.Of(7)

		policyProvider := createResPolicyProvider(cb, shortTimeout, longRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: true},
				{EntryId: "9999999i", IsError: true},
				{EntryId: "10101010j", IsError: true},
			},
		}
		assert.Len(t, *b, 10)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = ps.applyBulkSubscribeResiliency(context.TODO(), &in.bscData, in.pbsm, "dlq", orders1, policyDef, true, in.envelope)

		assert.Len(t, *b, 10)
		require.Error(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})
}

func assertRetryCount(t *testing.T, expectedIDRetryCountMap map[string]int, actualRetryCountMap map[string]int) {
	for k, v := range expectedIDRetryCountMap {
		assert.Equal(t, v, actualRetryCountMap[k], "expected retry/try count to match")
	}
}
