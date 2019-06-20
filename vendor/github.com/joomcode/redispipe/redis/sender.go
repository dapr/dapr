package redis

import (
	"errors"
)

// Sender is interface of client implementation.
// It provides interface in term of Future, and could be either single connection,
// connection to cluster, or whatever.
type Sender interface {
	// Send sends request to redis. When response will arrive, cb.Resolve(result, n) will be called.
	// Note: cb.Resolve could be called before Send returns.
	Send(r Request, cb Future, n uint64)
	// SendMany sends many requests at once.
	// When responses will arrive, cb.Resolve will be called with distinct n values:
	// - first request's response will be passed as cb.Resolve(response, n)
	// - second request's response will be passed as cb.Resolve(response, n+1)
	// - third ... cb.Resolve(response, n+2)
	// Note: responses could arrive in arbitrary order.
	SendMany(r []Request, cb Future, n uint64)
	// SendTransaction sends several requests as MULTI+EXEC redis transaction.
	// Response will be passed only once as an array of responses to commands (as EXEC does)
	// cb.Resolve([]interface{res1, res2, res3, ...}, n)
	SendTransaction(r []Request, cb Future, n uint64)
	// Scanner returns scanner object that scans keyspace sequentially.
	Scanner(opts ScanOpts) Scanner
	// EachShard synchronously calls callback for each shard.
	// Single-connection client will call it only once, but clustered will call for every master.
	// If callback is called with error, it will not be called again.
	// If callback returns false, iteration stops.
	EachShard(func(Sender, error) bool)
	// Close closes client. All following requests will be immediately resolved with error.
	Close()
}

// Scanner is an object used for scanning redis key space. It is returned by Sender.Scanner().
type Scanner interface {
	// Next will call cb.Resolve(result, 0) where `results` is keys part of result of SCAN/HSCAN/SSCAN/ZSCAN
	// (ie iterator part is handled internally).
	// When iteration completes, cb.Resolve(nil, 0) will be called.
	Next(cb Future)
}

// ScanEOF is error returned by Sync wrappers when iteration exhausted.
var ScanEOF = errors.New("Iteration finished")

// tools for scanning

// ScanOpts is options for scanning
type ScanOpts struct {
	// Cmd - command to be sent. Could be 'SCAN', 'SSCAN', 'HSCAN', 'ZSCAN'
	// default is 'SCAN'
	Cmd string
	// Key - key for SSCAN, HSCAN and ZSCAN command
	Key string
	// Match - pattern for filtering keys
	Match string
	// Count - soft-limit of single *SCAN answer
	Count int
}

// Request returns corresponding request to be send.
// Used mostly internally
func (s ScanOpts) Request(it []byte) Request {
	if len(it) == 0 {
		it = []byte("0")
	}
	args := make([]interface{}, 0, 6)
	if s.Cmd == "" {
		s.Cmd = "SCAN"
	}
	if s.Cmd != "SCAN" {
		args = append(args, s.Key)
	}
	args = append(args, it)
	if s.Match != "" {
		args = append(args, "MATCH", s.Match)
	}
	if s.Count > 0 {
		args = append(args, "COUNT", s.Count)
	}
	return Request{s.Cmd, args}
}

// ScannerBase is internal "parent" object for scanner implementations
type ScannerBase struct {
	// ScanOpts - options for this scanning
	ScanOpts
	// Iter - current iterator state
	Iter []byte
	// Err - error occurred. Implementation should stop iteration if Err is nil.
	Err error
	cb  Future
}

// DoNext - perform next step of iteration - send corresponding *SCAN command
func (s *ScannerBase) DoNext(cb Future, snd Sender) {
	s.cb = cb
	snd.Send(s.ScanOpts.Request(s.Iter), s, 0)
}

// IterLast - return true if iterator is at the end of this server/key keyspace.
func (s *ScannerBase) IterLast() bool {
	return len(s.Iter) == 1 && s.Iter[0] == '0'
}

// Cancelled - implements Future.Cancelled method
func (s *ScannerBase) Cancelled() error {
	return s.cb.Cancelled()
}

// Resolve - implements Future.Resolve.
// Accepts result of *SCAN command, remembers error and iterator
// and calls Resolve on underlying future.
func (s *ScannerBase) Resolve(res interface{}, _ uint64) {
	var keys []string
	s.Iter, keys, s.Err = ScanResponse(res)
	cb := s.cb
	s.cb = nil
	if s.Err != nil {
		cb.Resolve(s.Err, 0)
	} else {
		cb.Resolve(keys, 0)
	}
}
