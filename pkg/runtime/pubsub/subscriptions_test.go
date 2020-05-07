package pubsub

import (
	"testing"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/stretchr/testify/assert"
)

var log = logger.NewLogger("dapr.test")

func TestFilterSubscriptions(t *testing.T) {
	subs := []Subscription{
		{
			Topic: "topic0",
			Route: "topic0",
		},
		{
			Topic: "topic1",
		},
		{
			Topic: "topic1",
			Route: "custom/topic1",
		},
	}

	subs = filterSubscriptions(subs, log)
	assert.Len(t, subs, 2)
	assert.Equal(t, "topic0", subs[0].Topic)
	assert.Equal(t, "topic1", subs[1].Topic)
	assert.Equal(t, "custom/topic1", subs[1].Route)

}
