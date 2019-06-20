package redis

import "strings"

// hackish case insensitive hash function
func fnv1a64NoCase(s string) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range []byte(s) {
		h ^= uint64(c) &^ 0x20
		h *= 1099511628211
	}
	return h
}

func makeSet(names []string) []uint64 {
	l := 1
	for ; l < len(names)*2; l *= 2 {
	}
	hsh := make([]uint64, l)
	for _, name := range names {
		h := fnv1a64NoCase(name)
		pos := int(h) & (l - 1)
		for hsh[pos] != 0 {
			pos = (pos + 1) & (l - 1)
		}
		hsh[pos] = h
	}
	return hsh
}

func checkSet(name string, hsh []uint64) bool {
	h := fnv1a64NoCase(name)
	msk := len(hsh) - 1
	pos := int(h)
	for {
		switch hsh[pos&msk] {
		case h:
			return true
		case 0:
			return false
		}
		pos++
	}
}

var replicaSafe = makeSet(strings.Split(
	"PING ECHO DUMP MEMORY EXISTS GET GETRANGE RANDOMKEY KEYS TYPE TTL PTTL "+
		"BITCOUNT BITPOS GETBIT "+
		"GEOHASH GEOPOS GEODIST GEORADIUS_RO GEORADIUSBYMEMBER_RO "+
		"HEXISTS HGET HGETALL HKEYS HLEN HMGET HSTRLEN HVALS "+
		"LINDEX LLEN LRANGE "+
		"PFCOUNT "+
		"SCARD SDIFF SINTER SISMEMBER SMEMBERS SRANDMEMBER STRLEN SUNION "+
		"ZCARD ZCOUNT ZLEXCOUNT ZRANGE ZRANGEBYLEX ZREVRANGEBYLEX "+
		"ZRANGEBYSCORE ZRANK ZREVRANGE ZREVRANGEBYSCORE ZREVRANK ZSCORE "+
		"XPENDING XREVRANGE XREAD XLEN ", " "))

// ReplicaSafe returns true if command is readonly and "safe to run on replica".
// Some commands like "scan" are not included, because their result could differ between
// master and replica.
func ReplicaSafe(name string) bool {
	return checkSet(name, replicaSafe)
}

var blocking = makeSet(strings.Split("BLPOP BRPOP BLPOPPUSH BZPOPMIN BZPOPMAX XREAD XREADGROUP SAVE WATCH", " "))

// Blocking returns true if command is known to be blocking.
// Blocking commands could stall whole pipeline and therefore affect other commands sent
// through this connection. It is undesirable and prevented by default.
//
// This commands are forbidden in default configuration, but could be enabled with `SingleThreaded`
// connection option.
//
// `WATCH` command is also included here because while it is dangerous in concurrent environment,
// it is safe to be used in single threaded case.
func Blocking(name string) bool {
	return checkSet(name, blocking)
}

var subscribeHash = fnv1a64NoCase("SUBSCRIBE")
var psubscribeHash = fnv1a64NoCase("PSUBSCRIBE")

// Dangerous returns true if command is not safe to use with the connector.
// Currently it includes `SUBSCRIBE`, `PSUBSCRIBE` commands, because they changes connection protocol mode.
func Dangerous(name string) bool {
	h := fnv1a64NoCase(name)
	return h == subscribeHash || h == psubscribeHash
}

// ForbiddenCommand returns true if command is not allowed to run.
func ForbiddenCommand(name string, singleThreaded bool) error {
	h := fnv1a64NoCase(name)
	if h == subscribeHash || h == psubscribeHash {
		return ErrCommandForbidden.New("command %s could not be used with this connector", name)
	}
	if !singleThreaded && checkSet(name, blocking) {
		return ErrCommandForbidden.New("blocking command %s could be used only in 'scripting mode'", name)
	}
	return nil
}
