package relay

import (
	"io"
	"sync"
	"sync/atomic"
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
