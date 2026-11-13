package guac

import (
	"context"
	"io"
	"time"
)

type instructionResult struct {
	parts []string
	err   error
}

// BatchReader preserves instruction order and sync boundaries, with at most one
// instruction of read-ahead. The owner must close the underlying connection when
// ctx is cancelled to unblock a pending socket read.
type BatchReader struct {
	ctx     context.Context
	input   <-chan instructionResult
	pending *instructionResult
}

func NewBatchReader(ctx context.Context, reader *Reader) *BatchReader {
	input := make(chan instructionResult)
	go func() {
		defer close(input)
		for {
			p, e := reader.Read()
			select {
			case input <- instructionResult{p, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	return &BatchReader{ctx: ctx, input: input}
}

func (r *BatchReader) Read() ([][]string, error) {
	var first instructionResult
	if r.pending != nil {
		first = *r.pending
		r.pending = nil
	} else {
		select {
		case item, ok := <-r.input:
			if !ok {
				return nil, io.EOF
			}
			first = item
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
	if first.err != nil {
		return nil, first.err
	}
	batch := [][]string{first.parts}
	size := len(Encode(first.parts...))
	if first.parts[0] == "sync" || size >= 64*1024 {
		return batch, nil
	}
	timer := time.NewTimer(8 * time.Millisecond)
	defer timer.Stop()
	for len(batch) < 128 {
		select {
		case next, ok := <-r.input:
			if !ok {
				next.err = io.EOF
			}
			if next.err != nil {
				r.pending = &next
				return batch, nil
			}
			n := len(Encode(next.parts...))
			if size+n > 64*1024 {
				r.pending = &next
				return batch, nil
			}
			batch = append(batch, next.parts)
			size += n
			if next.parts[0] == "sync" {
				return batch, nil
			}
		case <-timer.C:
			return batch, nil
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
	return batch, nil
}
