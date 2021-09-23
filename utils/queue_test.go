package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueue(t *testing.T) {
	queue := NewBlockQueue()

	queue.Offer(123)

	assert.Equal(t, 123, queue.Poll())

	go func() {
		queue.Offer(1)
		queue.Offer(2)
		queue.Offer(3)
	}()

	assert.Equal(t, 1, queue.Poll())
	assert.Equal(t, 2, queue.Poll())
	assert.Equal(t, 3, queue.Poll())

	start := time.Now()
	queue.Offer(1)
	go func() {
		time.Sleep(time.Second * 3)
		queue.Poll()
	}()

	queue.BlockUntilEmpty()

	end := time.Now()
	assert.True(t, end.Sub(start).Seconds() >= 3)
}
