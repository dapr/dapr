package fasthttp

import (
	"sync/atomic"
	"time"
)

// CoarseTimeNow returns the current time truncated to the nearest second.
//
// This is a faster alternative to time.Now().
func CoarseTimeNow() time.Time {
	tp := coarseTime.Load().(*time.Time)
	return *tp
}

func init() {
	t := time.Now().Truncate(time.Second)
	coarseTime.Store(&t)
	go func() {
		for {
			time.Sleep(time.Second)
			t := time.Now().Truncate(time.Second)
			coarseTime.Store(&t)
		}
	}()
}

var coarseTime atomic.Value
