/*
Package redis contains common parts for other packages.

- main interfaces visible to user (Sender, Scanner, ScanOpts)

- wrappers for synchronous interface over Sender (Sync, SyncCtx)
and chan-based-future interface (ChanFutured)

- request writing,

- response parsing,

- root errorx namespace and common error types.

Usually you get Sender from redisconn.Connect or rediscluster.NewCluster, then wrap with Sync or SyncCtx, and use their
sync methods without any locking:

	sender, err := redisconn.Connect(ctx, "127.0.0.1:6379", redisconn.Opts{})
	sync := redis.Sync{sender}
	go func() {
		res := sync.Do("GET", "x")
		if err := redis.AsError(res); err != nil {
			log.Println("failed", err)
		}
		log.Println("found x", res)
	}()
	go func() {
		results := sync.SendMany([]redis.Request{
			redis.Req("GET", "k1"),
			redis.Req("Incr", "k2"),
			redis.Req("HMGET, "h1", "hk1", "hk2"),
		})
		if err := redis.AsError(results[0]); err != nil {
			log.Println("failed", err)
		}
		if results[0] == nil {
			log.Println("not found")
		} else {
			log.Println("k1: ", results[0])
		}
	}()

See more documentation in root redispipe package.
*/
package redis
