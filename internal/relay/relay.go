package relay

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

func Relay(a, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(a, b)
		a.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(b, a)
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
		io.Copy(a, &countingReader{r: b, n: bToA})
		a.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(b, &countingReader{r: a, n: aToB})
		b.Close()
	}()

	wg.Wait()
}

type rateLimitedReader struct {
	r         io.Reader
	n         *atomic.Int64
	bucket    int64
	limit     int64 // bytes per second
	lastFill  time.Time
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	if r.limit > 0 {
		now := time.Now()
		elapsed := now.Sub(r.lastFill).Seconds()
		r.bucket += int64(elapsed * float64(r.limit))
		if r.bucket > r.limit {
			r.bucket = r.limit
		}
		r.lastFill = now
		if r.bucket <= 0 {
			time.Sleep(time.Millisecond * 10)
			r.bucket += int64(0.01 * float64(r.limit))
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
		io.Copy(a, &rateLimitedReader{r: b, n: bToA, limit: bytesPerSec, bucket: bytesPerSec, lastFill: now})
		a.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(b, &rateLimitedReader{r: a, n: aToB, limit: bytesPerSec, bucket: bytesPerSec, lastFill: now})
		b.Close()
	}()

	wg.Wait()
}
