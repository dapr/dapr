package concurrency

import (
	"testing"
)

func TestLimiter(t *testing.T) {
	limit := NewLimiter(10)
	for i := 0; i < 1000; i++ {
		limit.Execute(func(_ interface{}) {
			// do some work
		}, nil)
	}
	limit.Wait()
}
