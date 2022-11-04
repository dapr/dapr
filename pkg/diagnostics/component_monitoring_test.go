package diagnostics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/instrument"
)

type mockedCount struct {
	instrument.Synchronous
	count int64
	attrs []attribute.KeyValue
}

// Add records a change to the counter.
func (m *mockedCount) Add(ctx context.Context, incr int64, attrs ...attribute.KeyValue) {
	m.count += incr
	m.attrs = append(m.attrs, attrs...)

	return
}

type mockedHistogram struct {
	instrument.Synchronous
	elapsed float64
	attrs   []attribute.KeyValue
}

// Record adds an additional value to the distribution.
func (m *mockedHistogram) Record(ctx context.Context, incr float64, attrs ...attribute.KeyValue) {
	m.elapsed += incr
	m.attrs = append(m.attrs, attrs...)

	return
}

func TestPubsubIngressEvent(t *testing.T) {

}

func TestBulkPubsubIngressEvent(t *testing.T) {

}
