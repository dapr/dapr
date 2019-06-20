package redisconn

import (
	"github.com/joomcode/redispipe/redis"
)

// Scanner is an implementation of redis.Scanner
type Scanner struct {
	redis.ScannerBase
	c *Connection
}

// Next is an implementation of redis.Scanner.Next
func (s *Scanner) Next(cb redis.Future) {
	if s.Err != nil {
		cb.Resolve(s.Err, 0)
		return
	}
	if s.IterLast() {
		cb.Resolve(nil, 0)
		return
	}
	s.DoNext(cb, s.c)
}

// Scanner implements redis.Sender.Scanner
func (c *Connection) Scanner(opts redis.ScanOpts) redis.Scanner {
	return &Scanner{
		ScannerBase: redis.ScannerBase{ScanOpts: opts},
		c:           c,
	}
}
