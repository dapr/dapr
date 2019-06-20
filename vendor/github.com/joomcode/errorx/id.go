package errorx

import (
	"sync/atomic"
)

var internalID uint64

// nextInternalID creates next unique id for errorx entities.
// All equality comparison should take id into account, lest there be some false positive matches.
func nextInternalID() uint64 {
	return atomic.AddUint64(&internalID, 1)
}
