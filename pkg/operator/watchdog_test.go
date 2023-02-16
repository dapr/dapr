package operator

import (
	"context"
	"testing"
	"time"

	"go.uber.org/ratelimit"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDaprWatchdog_listPods(t *testing.T) {
	type fields struct {
		interval          time.Duration
		maxRestartsPerMin int
		client            client.Client
		restartLimiter    ratelimit.Limiter
	}
	type args struct {
		ctx                                  context.Context
		podsNotMatchingInjectorLabelSelector labels.Selector
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			// TODO: Add test cases.}
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dw := &DaprWatchdog{
				interval:          tt.fields.interval,
				maxRestartsPerMin: tt.fields.maxRestartsPerMin,
				client:            tt.fields.client,
				restartLimiter:    tt.fields.restartLimiter,
			}
			if got := dw.listPods(tt.args.ctx, tt.args.podsNotMatchingInjectorLabelSelector); got != tt.want {
				t.Errorf("listPods() = %v, want %v", got, tt.want)
			}
		})
	}
}
