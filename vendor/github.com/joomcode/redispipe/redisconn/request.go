package redisconn

import (
	"time"

	"github.com/joomcode/redispipe/redis"
)

// Request is an alias for redis.Request
type Request = redis.Request

// Future is an alias for redis.Future
type Future = redis.Future

type future struct {
	Future
	N uint64

	start int64
	req   Request
}

var epoch = time.Now()

func nownano() int64 {
	return int64(time.Now().Sub(epoch))
}

func (c *Connection) resolve(f future, res interface{}) {
	if f.start != 0 && f.req.Cmd != "" {
		c.opts.Logger.ReqStat(c, f.req, res, nownano()-f.start)
	}
	f.Future.Resolve(res, f.N)
}
