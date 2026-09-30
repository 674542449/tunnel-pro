package relay

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const copyBufSize = 64 * 1024

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, copyBufSize)
		return &b
	},
}

func Relay(a, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(a, b, *bp)
		bufPool.Put(bp)
		a.Close()
	}()

	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(b, a, *bp)
		bufPool.Put(bp)
		b.Close()
	}()

	wg.Wait()
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

func CountingRelay(a, b io.ReadWriteCloser, aToB, bToA *atomic.Int64) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(a, &countingReader{r: b, n: bToA}, *bp)
		bufPool.Put(bp)
		a.Close()
	}()

	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(b, &countingReader{r: a, n: aToB}, *bp)
		bufPool.Put(bp)
		b.Close()
	}()

	wg.Wait()
}

type rateLimitedReader struct {
	r        io.Reader
	n        *atomic.Int64
	bucket   int64
	limit    int64 // bytes per second
	lastFill time.Time
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	if r.limit > 0 {
		now := time.Now()
		elapsed := now.Sub(r.lastFill).Seconds()
		r.bucket += int64(elapsed * float64(r.limit))
		if r.bucket > r.limit*2 {
			r.bucket = r.limit * 2
		}
		r.lastFill = now
		if r.bucket <= 0 {
			time.Sleep(time.Millisecond * 5)
			r.bucket += int64(0.005 * float64(r.limit))
		}
		if int64(len(p)) > r.bucket {
			p = p[:r.bucket]
		}
	}
	n, err := r.r.Read(p)
	if n > 0 {
		if r.n != nil {
			r.n.Add(int64(n))
		}
		if r.limit > 0 {
			r.bucket -= int64(n)
		}
	}
	return n, err
}

func RateLimitedRelay(a, b io.ReadWriteCloser, aToB, bToA *atomic.Int64, bytesPerSec int64) {
	var wg sync.WaitGroup
	wg.Add(2)

	now := time.Now()
	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(a, &rateLimitedReader{r: b, n: bToA, limit: bytesPerSec, bucket: bytesPerSec * 2, lastFill: now}, *bp)
		bufPool.Put(bp)
		a.Close()
	}()

	go func() {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		io.CopyBuffer(b, &rateLimitedReader{r: a, n: aToB, limit: bytesPerSec, bucket: bytesPerSec * 2, lastFill: now}, *bp)
		bufPool.Put(bp)
		b.Close()
	}()

	wg.Wait()
}
