package redis

import (
	"sync"

	"github.com/joomcode/errorx"
)

// Sync provides convenient synchronouse interface over asynchronouse Sender.
type Sync struct {
	S Sender
}

// Do is convenient method to construct and send request.
// Returns value that could be either result or error.
func (s Sync) Do(cmd string, args ...interface{}) interface{} {
	return s.Send(Request{cmd, args})
}

// Send sends request to redis.
// Returns value that could be either result or error.
func (s Sync) Send(r Request) interface{} {
	var res syncRes
	res.Add(1)
	s.S.Send(r, &res, 0)
	res.Wait()
	if CollectTrace {
		if err := AsErrorx(res.r); err != nil {
			res.r = errorx.EnsureStackTrace(err)
		}
	}
	return res.r
}

// SendMany sends several requests in "parallel" and returns slice or results in a same order.
// Each result could be value or error.
func (s Sync) SendMany(reqs []Request) []interface{} {
	if len(reqs) == 0 {
		return nil
	}

	res := syncBatch{
		r: make([]interface{}, len(reqs)),
	}
	res.Add(len(reqs))
	s.S.SendMany(reqs, &res, 0)
	res.Wait()
	if CollectTrace {
		for i, v := range res.r {
			if err := AsErrorx(v); err != nil {
				res.r[i] = errorx.EnsureStackTrace(err)
			}
		}
	}
	return res.r
}

// SendTransaction sends several requests as a single MULTI+EXEC transaction.
// It returns array of responses and an error, if transaction fails.
// Since Redis transaction either fully executed or fully failed,
// all values are valid if err == nil.
func (s Sync) SendTransaction(reqs []Request) ([]interface{}, error) {
	var res syncRes
	res.Add(1)
	s.S.SendTransaction(reqs, &res, 0)
	res.Wait()
	ress, err := TransactionResponse(res.r)
	if CollectTrace && err != nil {
		err = errorx.EnsureStackTrace(err)
	}
	return ress, err
}

// Scanner returns synchronous iterator over redis keyspace/key.
func (s Sync) Scanner(opts ScanOpts) SyncIterator {
	return SyncIterator{s.S.Scanner(opts)}
}

type syncRes struct {
	r interface{}
	sync.WaitGroup
}

// Cancelled implements Future.Cancelled
func (s *syncRes) Cancelled() error {
	return nil
}

// Resolve implements Future.Resolve
func (s *syncRes) Resolve(res interface{}, _ uint64) {
	s.r = res
	s.Done()
}

type syncBatch struct {
	r []interface{}
	sync.WaitGroup
}

// Cancelled implements Future.Cancelled
func (s *syncBatch) Cancelled() error {
	return nil
}

// Resolve implements Future.Resolve
func (s *syncBatch) Resolve(res interface{}, i uint64) {
	s.r[i] = res
	s.Done()
}

// SyncIterator is synchronous iterator over repeating *SCAN command.
type SyncIterator struct {
	s Scanner
}

// Next returns next bunch of keys, or error.
// ScanEOF error signals for regular iteration completion.
func (s SyncIterator) Next() ([]string, error) {
	var res syncRes
	res.Add(1)
	s.s.Next(&res)
	res.Wait()
	if err := AsError(res.r); err != nil {
		if CollectTrace {
			err = errorx.EnsureStackTrace(err.(*errorx.Error))
		}
		return nil, err
	} else if res.r == nil {
		return nil, ScanEOF
	} else {
		return res.r.([]string), nil
	}
}
