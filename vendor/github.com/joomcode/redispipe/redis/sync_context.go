package redis

import (
	"context"
	"sync/atomic"

	"github.com/joomcode/errorx"
)

// SyncCtx (like Sync) provides convenient synchronous interface over asynchronous Sender.
// Its methods accept context.Context to allow early request cancelling.
// Note that if context were cancelled after request were send, redis still will execute it,
// but you will have no way to know about that fact.
type SyncCtx struct {
	S Sender
}

// Do is convenient method to construct and send request.
// Returns value that could be either result or error.
// When context is cancelled, Do returns ErrRequestCancelled error.
func (s SyncCtx) Do(ctx context.Context, cmd string, args ...interface{}) interface{} {
	return s.Send(ctx, Request{cmd, args})
}

// Send sends request to redis.
// Returns value that could be either result or error.
// When context is cancelled, Send returns ErrRequestCancelled error.
func (s SyncCtx) Send(ctx context.Context, r Request) interface{} {
	res := ctxRes{active: newActive(ctx)}

	s.S.Send(r, &res, 0)

	select {
	case <-ctx.Done():
		err := ErrRequestCancelled.WrapWithNoMessage(ctx.Err())
		if CollectTrace {
			err = errorx.EnsureStackTrace(err)
		}
		return err
	case <-res.ch:
		if CollectTrace {
			if err := AsError(res.r); err != nil {
				res.r = errorx.EnsureStackTrace(err)
			}
		}
		return res.r
	}
}

// SendMany sends several requests in "parallel" and returns slice or results in a same order.
// Each result could be value or error.
// When context is cancelled, SendMany returns slice of ErrRequestCancelled errors.
func (s SyncCtx) SendMany(ctx context.Context, reqs []Request) []interface{} {
	if len(reqs) == 0 {
		return nil
	}

	res := ctxBatch{
		active: newActive(ctx),
		r:      make([]interface{}, len(reqs)),
		o:      make([]uint32, len(reqs)),
		cnt:    0,
	}

	s.S.SendMany(reqs, &res, 0)

	select {
	case <-ctx.Done():
		err := ErrRequestCancelled.WrapWithNoMessage(ctx.Err())
		if CollectTrace {
			err = errorx.EnsureStackTrace(err)
		}
		for i := range res.r {
			res.Resolve(err, uint64(i))
		}
		<-res.ch
	case <-res.ch:
	}
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
// all values are valid if err == nil. But some of them could be error on their own.
// When context is cancelled, SendTransaction returns ErrRequestCancelled error.
func (s SyncCtx) SendTransaction(ctx context.Context, reqs []Request) ([]interface{}, error) {
	res := ctxRes{active: newActive(ctx)}

	s.S.SendTransaction(reqs, &res, 0)

	var r interface{}
	select {
	case <-ctx.Done():
		r = ErrRequestCancelled.WrapWithNoMessage(ctx.Err())
	case <-res.ch:
		r = res.r
	}

	ress, err := TransactionResponse(r)
	if CollectTrace && err != nil {
		err = errorx.EnsureStackTrace(err)
	}
	return ress, err
}

// Scanner returns synchronous iterator over redis keyspace/key.
// Scanner will stop iteration if context were cancelled.
func (s SyncCtx) Scanner(ctx context.Context, opts ScanOpts) SyncCtxIterator {
	return SyncCtxIterator{ctx, s.S.Scanner(opts)}
}

type active struct {
	ctx context.Context
	ch  chan struct{}
}

func newActive(ctx context.Context) active {
	return active{ctx, make(chan struct{})}
}

// Cancelled implements Future.Cancelled
func (c active) Cancelled() error {
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return nil
	}
}

func (c active) done() {
	close(c.ch)
}

type ctxRes struct {
	active
	r interface{}
}

// Resolve implements Future.Resolve
func (c *ctxRes) Resolve(r interface{}, _ uint64) {
	c.r = r
	c.done()
}

type ctxBatch struct {
	active
	r   []interface{}
	o   []uint32
	cnt uint32
}

// Resolve implements Future.Resolve
func (s *ctxBatch) Resolve(res interface{}, i uint64) {
	if atomic.CompareAndSwapUint32(&s.o[i], 0, 1) {
		s.r[i] = res
		if int(atomic.AddUint32(&s.cnt, 1)) == len(s.r) {
			s.done()
		}
	}
}

// SyncCtxIterator is synchronous iterator over repeating *SCAN command.
// It will stop iteration if context were cancelled.
type SyncCtxIterator struct {
	ctx context.Context
	s   Scanner
}

// Next returns next bunch of keys, or error.
// ScanEOF error signals for regular iteration completion.
// It will return ErrRequestCancelled error if context were cancelled.
func (s SyncCtxIterator) Next() ([]string, error) {
	res := ctxRes{active: newActive(s.ctx)}
	s.s.Next(&res)
	select {
	case <-s.ctx.Done():
		err := ErrRequestCancelled.WrapWithNoMessage(s.ctx.Err())
		if CollectTrace {
			err = errorx.EnsureStackTrace(err)
		}
		return nil, err
	case <-res.ch:
	}
	if err := AsError(res.r); err != nil {
		if CollectTrace {
			err = errorx.EnsureStackTrace(err)
		}
		return nil, err
	} else if res.r == nil {
		return nil, ScanEOF
	} else {
		return res.r.([]string), nil
	}
}
