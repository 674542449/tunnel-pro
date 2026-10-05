package integration

import (
	"bufio"
	"io"
	"net/url"
)

func bufReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }
func urlParse(s string) (*url.URL, error) { return url.Parse(s) }
