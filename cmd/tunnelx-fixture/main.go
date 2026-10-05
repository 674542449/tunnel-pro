package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	video := flag.String("video-file", "/opt/tunnelx/fixtures/test-video.mp4", "controlled video file")
	flag.Parse()
	u, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 18481})
	if e != nil {
		panic(e)
	}
	go func() {
		b := make([]byte, 65535)
		for {
			n, a, e := u.ReadFromUDP(b)
			if e != nil {
				return
			}
			u.WriteToUDP(b[:n], a)
		}
	}()
	ln, e := net.Listen("tcp", "127.0.0.1:18482")
	if e != nil {
		panic(e)
	}
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(60 * time.Second))
				h := sha256.New()
				n, e := io.Copy(h, io.LimitReader(c, 64<<20))
				if e == nil {
					fmt.Fprintf(c, "%d %s\n", n, hex.EncodeToString(h.Sum(nil)))
				}
			}()
		}
	}()
	http.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		size := 64 << 20
		fmt.Sscan(r.URL.Query().Get("size"), &size)
		if size < 0 || size > 256<<20 {
			http.Error(w, "Bad size", 400)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.Header().Set("Content-Type", "application/octet-stream")
		b := make([]byte, 32<<10)
		for i := range b {
			b[i] = byte(i % 251)
		}
		for left := size; left > 0; {
			n := len(b)
			if left < n {
				n = left
			}
			if _, e := w.Write(b[:n]); e != nil {
				return
			}
			left -= n
		}
	})
	http.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, e := io.Copy(h, http.MaxBytesReader(w, r.Body, 64<<20))
		if e != nil {
			http.Error(w, "Upload too large", 413)
			return
		}
		fmt.Fprintf(w, "%d %s\n", n, hex.EncodeToString(h.Sum(nil)))
	})
	http.HandleFunc("/video", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, *video) })
	fmt.Fprintln(os.Stdout, "Acceptance fixture listening on loopback 18480/TCP, 18481/UDP, 18482/TCP")
	if e = http.ListenAndServe("127.0.0.1:18480", nil); e != nil {
		panic(e)
	}
}
