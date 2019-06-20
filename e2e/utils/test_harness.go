package utils

import (
	"strings"
	"testing"
	"time"
)

type OutputAnalyzer struct{}

type TestResult struct {
	Receviced           int
	Sent                int
	Lost                int
	Duplicated          int
	LostMessages        []string
	Throughput          float64
	FirstMessageSent    time.Time
	LastMessageReceived time.Time
}

func (*OutputAnalyzer) LogTestResult(t *testing.T, result TestResult) {
	t.Logf("Messages sent: %d", result.Sent)
	t.Logf("Messages received: %d", result.Receviced)
	t.Logf("Duplicated messages: %d", result.Duplicated)
	t.Logf("Lost messages: %d", result.Lost)
	t.Logf("Throughput: %f", result.Throughput)
	t.Logf("First sent: %s", result.FirstMessageSent.Format(time.RFC3339))
	t.Logf("Last recevied: %s", result.LastMessageReceived.Format(time.RFC3339))
}
func (*OutputAnalyzer) AnalyzeOutputs(outputs map[string][]string, senderKey string, receiverKey string) (TestResult, error) {
	sent := len(outputs[senderKey])
	received := len(outputs[receiverKey])
	duplicated := 0
	lost := 0
	firstMessageSent, _ := time.Parse(time.RFC3339, "2050-12-04T00:00:00-00:00")
	lastMessageReceived := time.Time{}
	lostMessages := make([]string, 0)

	for _, s := range outputs[senderKey] {
		parts := strings.Split(s, "#")
		if len(parts) > 1 {
			t, err := time.Parse(time.RFC3339, parts[len(parts)-1])
			if err == nil && t.Before(firstMessageSent) {
				firstMessageSent = t
			}
		}
	}
	for _, s := range outputs[receiverKey] {
		parts := strings.Split(s, "#")
		if len(parts) > 1 {
			t, err := time.Parse(time.RFC3339, parts[len(parts)-1])
			if err == nil && t.After(lastMessageReceived) {
				lastMessageReceived = t
			}
		}
	}

	for _, s := range outputs[senderKey] {
		count := 0
		for _, r := range outputs[receiverKey] {
			if strings.Split(s, "#")[0] == strings.Split(r, "#")[0] {
				count++
			}
		}
		if count == 0 {
			lostMessages = append(lostMessages, s)
			lost++
		} else if count != 1 {
			duplicated += (count - 1)
		}
	}

	duration := lastMessageReceived.Sub(firstMessageSent)
	var throughput float64 = 0.0
	if duration.Seconds() > 0 {
		throughput = float64(received) / duration.Seconds()
	}
	return TestResult{
		Sent:                sent,
		Receviced:           received,
		Duplicated:          duplicated,
		Lost:                lost,
		LostMessages:        lostMessages,
		LastMessageReceived: lastMessageReceived,
		FirstMessageSent:    firstMessageSent,
		Throughput:          throughput,
	}, nil
}
