package actors

import (
	"sync"
	"time"
)

type actor struct {
	lock         *sync.RWMutex
	lastUsedTime time.Time
	busy         bool
	busyCh       chan (bool)
}
