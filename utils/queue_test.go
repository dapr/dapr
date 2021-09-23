package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueue(t *testing.T) {
	queue := NewBlockQueue()

	queue.Push(123)

	assert.Equal(t, 123, queue.Pop())

	go func() {
		queue.Push(1)
		queue.Push(2)
		queue.Push(3)
	}()

	assert.Equal(t, 1, queue.Pop())
	assert.Equal(t, 2, queue.Pop())
	assert.Equal(t, 3, queue.Pop())

	start := time.Now()
	queue.Push(1)
	go func() {
		time.Sleep(time.Second * 3)
		queue.Pop()
	}()

	queue.BlockUntilEmpty()

	end := time.Now()
	assert.True(t, end.Sub(start).Seconds() >= 3)
}
