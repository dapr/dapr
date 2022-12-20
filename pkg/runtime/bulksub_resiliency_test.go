//nolint:nosnakecase
package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var (
	testLogger = logger.NewLogger("dapr.test")
	// zero       = contribPubsub.BulkPublishResponse{}
)

type input struct {
	pbsm     pubsubBulkSubscribedMessage
	bscData  bulkSubscribeCallData
	envelope map[string]interface{}
}

type testSettings struct {
	entryIdRetryTimes map[string]int
	failEvenOnes      bool
	failAllEntries    bool
	failCount         int
}

const (
	d1    string = `{"orderId":"1"}`
	d2    string = `{"orderId":"2"}`
	d3    string = `{"orderId":"3"}`
	d4    string = `{"orderId":"4"}`
	d5    string = `{"orderId":"5"}`
	d6    string = `{"orderId":"6"}`
	d7    string = `{"orderId":"7"}`
	d8    string = `{"orderId":"8"}`
	d9    string = ``
	d10   string = `{"orderId":"10"}`
	ord1  string = `{"data":` + d1 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"9b6767c3-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	ord2  string = `{"data":` + d2 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"993f4e4a-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
	ord3  string = `{"data":` + d3 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"6767010u-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	ord4  string = `{"data":` + d4 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"91011121-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
	ord5  string = `{"data":` + d5 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"718271cd-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	ord6  string = `{"data":` + d6 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"7uw2233d-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
	ord7  string = `{"data":` + d7 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"78sqs98s-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	ord8  string = `{"data":` + d8 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"45122j82-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
	ord9  string = `{"` + ext1Key + `":"` + ext1Value + `","orderId":"9","type":"type1"}`
	ord10 string = `{"data":` + d10 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"ded2rd44-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
)

func getBulkMessageEntriesForResiliency(len int) []pubsub.BulkMessageEntry {
	bulkEntries := make([]pubsub.BulkMessageEntry, 10)

	bulkEntries[0] = pubsub.BulkMessageEntry{EntryId: "1111111a", Event: []byte(ord1)}
	bulkEntries[1] = pubsub.BulkMessageEntry{EntryId: "2222222b", Event: []byte(ord2)}
	bulkEntries[2] = pubsub.BulkMessageEntry{EntryId: "333333c", Event: []byte(ord3)}
	bulkEntries[3] = pubsub.BulkMessageEntry{EntryId: "4444444d", Event: []byte(ord4)}
	bulkEntries[4] = pubsub.BulkMessageEntry{EntryId: "5555555e", Event: []byte(ord5)}
	bulkEntries[5] = pubsub.BulkMessageEntry{EntryId: "66666666f", Event: []byte(ord6)}
	bulkEntries[6] = pubsub.BulkMessageEntry{EntryId: "7777777g", Event: []byte(ord7)}
	bulkEntries[7] = pubsub.BulkMessageEntry{EntryId: "8888888h", Event: []byte(ord8)}
	bulkEntries[8] = pubsub.BulkMessageEntry{EntryId: "9999999i", Event: []byte(ord9)}
	bulkEntries[9] = pubsub.BulkMessageEntry{EntryId: "10101010j", Event: []byte(ord10)}

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
var longTimeout = "10s"
var shortTimeout = "1s"

var orders []string = []string{ord1, ord2, ord3, ord4, ord5, ord6, ord7, ord8, ord9, ord10}

func getPubSubMessages() []pubSubMessage {
	pubSubMessages := make([]pubSubMessage, 10)

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
	retry resiliencyV1alpha.Retry) *resiliency.Resiliency {
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
	var data map[string]interface{}
	e := json.Unmarshal(req.Message().Data.Value, &data)
	appResponses := []pubsub.AppBulkResponseEntry{}
	if e == nil {
		entries := data["entries"].([]interface{})
		for j := 1; j <= len(entries); j++ {
			entryId := entries[j-1].(map[string]interface{})["entryId"].(string)
			abre := pubsub.AppBulkResponseEntry{
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
	re := pubsub.AppBulkResponse{
		AppResponses: appResponses,
	}
	resp, _ := json.Marshal(re)
	respInvoke := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	respInvoke.WithRawData(resp, "application/json")
	return respInvoke
}

func getInput() input {
	in := input{}
	testBulkSubscribePubsub := "bulkSubscribePubSub"
	msgArr := getBulkMessageEntriesForResiliency(10)
	psMessages := getPubSubMessages()
	in.pbsm = pubsubBulkSubscribedMessage{
		pubSubMessages: psMessages,
		topic:          "topic0",
		pubsub:         testBulkSubscribePubsub,
		path:           "orders1",
		length:         len(psMessages),
	}

	bulkResponses := make([]pubsub.BulkSubscribeResponseEntry, 10)
	in.bscData.bulkResponses = &bulkResponses
	entryIdIndexMap := make(map[string]int)
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
	in.bscData.ctx = context.TODO()
	return in
}

func TestBulkSubscribeResiliency(t *testing.T) {

	t.Run("verify Responses when few entries fail even after retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         4,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Equal(t, 10, len(*b))

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
		assert.NotNil(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when ALL entries fail even after retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         4,
			failEvenOnes:      true,
			failAllEntries:    true,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Equal(t, 10, len(*b))

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
		assert.NotNil(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("pass ALL entries in second attempt", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         1,
			failEvenOnes:      false,
			failAllEntries:    true,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Equal(t, 10, len(*b))

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
		assert.Nil(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("pass ALL entries in first attempt", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         0,
			failEvenOnes:      false,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		// After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, longTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
		assert.Equal(t, 10, len(*b))

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
		assert.Nil(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("fail ALL entries due to timeout", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         0,
			failEvenOnes:      false,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" })).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
			mockee.ReturnArguments = mock.Arguments{respInvoke1, nil}
		}
		shortRetry.MaxRetries = ptr.Of(2)

		policyProvider := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, shortTimeout, shortRetry)
		policyDef := policyProvider.ComponentInboundPolicy(pubsubName, resiliency.Pubsub)
		in := getInput()
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		assert.Equal(t, 10, len(*b))

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
		assert.NotNil(t, e)
		assert.ErrorIs(t, e, context.DeadlineExceeded)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when ALL entries fail with Circuitbreaker and exhaust retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    true,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when Partial entries fail with Circuitbreaker and exhaust retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("verify Responses when Partial entries Pass with Circuitbreaker half open timeout", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         2,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.Nil(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

	t.Run("Partial success with CB and exhaust retries, then act with short half open timeout", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         2,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)

		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.Nil(t, e)
		// assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

	})

	t.Run("Fail all events with timeout and then Open CB - short retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" })).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		assert.Equal(t, 10, len(*b))
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

	})
}
func TestBulkSubscribeResiliencyStateConversionsFromHalfOpen(t *testing.T) {
	t.Run("verify Responses when Circuitbreaker half open state changes happen", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         3,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" }))
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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

		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)
		// after this time, circuit breaker should be half-open
		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		// circuit breaker is open, so no call should go through
		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		time.Sleep(5 * time.Second)
		// after this time, circuit breaker should be half-open
		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assertRetryCount(t, expectedCBRetryCount, ts.entryIdRetryTimes)
		assert.Nil(t, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})

}

func TestBulkSubscribeResiliencyWithLongRetries(t *testing.T) {
	t.Run("Fail all events with timeout and then Open CB - long retries", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		rt.appChannel = mockAppChannel

		ts := testSettings{
			entryIdRetryTimes: map[string]int{},
			failCount:         10,
			failEvenOnes:      true,
			failAllEntries:    false,
		}

		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		mockee := mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.MatchedBy(
			func(req *invokev1.InvokeMethodRequest) bool { return req.Message().Method == "orders1" })).
			After(3 * time.Second)
		mockee.RunFn = func(args mock.Arguments) {
			respInvoke1 = getResponse(args.Get(1).(*invokev1.InvokeMethodRequest), &ts)
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
		b, e := rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

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
		assert.Equal(t, 10, len(*b))
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))

		b, e = rt.ApplyBulkSubscribeResiliency(&in.bscData, in.pbsm, "dlq", "orders1", policyDef, true, in.envelope)

		assert.Equal(t, 10, len(*b))
		assert.NotNil(t, e)
		assert.Equal(t, breaker.ErrOpenState, e)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, *b))
	})
}

func assertRetryCount(t *testing.T, expectedIDRetryCountMap map[string]int, actualRetryCountMap map[string]int) {
	for k, v := range expectedIDRetryCountMap {
		assert.Equal(t, v, actualRetryCountMap[k], "expected retry/try count to match")
	}
}
