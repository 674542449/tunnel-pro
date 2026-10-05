package server

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/http2"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"tunnelx/internal/config"
	"tunnelx/internal/wire"
)

type Server struct {
	Config         config.Server
	auth           *config.Auth
	slots          chan struct{}
	HTTP           *http.Server
	TargetAttempts atomic.Int64
	Active         atomic.Int64
	Resolver       func(context.Context, string) ([]net.IP, error)
	Access         AccessController
}

func New(c config.Server) (*Server, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	tlsCfg, e := c.TLS()
	if e != nil {
		return nil, e
	}
	s := &Server{Config: c, auth: config.NewAuth(c.Tokens), slots: make(chan struct{}, c.MaxConnections)}
	s.Resolver = DefaultLookup
	_ = tlsCfg
	return s, nil
}

func (s *Server) Serve(ctx context.Context) error {
	tlsCfg, e := s.Config.TLS()
	if e != nil {
		return e
	}
	tlsCfg.NextProtos = []string{"h2", "http/1.1"}
	s.HTTP = &http.Server{Addr: s.Config.Listen, TLSConfig: tlsCfg, Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10}
	if e = http2.ConfigureServer(s.HTTP, &http2.Server{MaxConcurrentStreams: 128, MaxUploadBufferPerConnection: 4 << 20, MaxUploadBufferPerStream: 1 << 20, IdleTimeout: 120 * time.Second}); e != nil {
		return e
	}
	errs := make(chan error, 1)
	go func() { errs <- s.HTTP.ListenAndServeTLS("", "") }()
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	// Close cancels live tunnels; do not wait indefinitely for long-lived CONNECT streams.
	s.HTTP.Close()
	return e
}
func (s *Server) public(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	if s.Config.PublicDir != "" {
		http.FileServer(http.Dir(s.Config.PublicDir)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == "GET" {
		io.WriteString(w, "<!doctype html><html><meta charset=utf-8><meta name=viewport content='width=device-width'><title>Network service</title><body><main><h1>Network service</h1><p>This HTTPS service is online.</p></main></body></html>")
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Config.ManagedOnly || !s.auth.Valid(r.Header.Values("Authorization")) {
		var p Permit
		var ok bool
		if s.Access != nil {
			p, ok = s.Access.Authorize(r.Context(), r.Header.Values("Authorization"), r.Header.Get("Tunnelx-Device"))
		}
		if !ok {
			s.public(w, r)
			return
		}
		defer p.Close()
		r = r.WithContext(context.WithValue(p.Context(), permitKey{}, p))
	}
	w.Header().Set("Tunnelx-Version", config.Version)
	if r.Header.Get("Tunnelx-Version") != config.Version {
		http.Error(w, "Bad Request", 400)
		return
	}
	if r.Method != "CONNECT" {
		s.public(w, r)
		return
	}
	if r.ProtoMajor != 2 {
		http.Error(w, "HTTP Version Not Supported", 505)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "Too Many Requests", 429)
		return
	}
	s.Active.Add(1)
	defer s.Active.Add(-1)
	proto := r.Header.Get(":protocol")
	target := r.Host
	if proto != "" {
		if proto != "connect-udp" {
			http.Error(w, "Not Implemented", 501)
			return
		}
		var e error
		target, e = UDPAddress(r.URL)
		if e != nil {
			http.Error(w, "Bad Request", 400)
			return
		}
		if r.Header.Get("Capsule-Protocol") != "?1" {
			http.Error(w, "Bad Request", 400)
			return
		}
	}
	host, port, e := net.SplitHostPort(target)
	p, pe := strconv.Atoi(port)
	if e != nil || pe != nil || p < 1 || p > 65535 || host == "" {
		http.Error(w, "Bad Request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.Config.DialTimeout)*time.Second)
	defer cancel()
	addresses, status := s.resolve(ctx, host, target)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if proto == "connect-udp" {
		s.udp(w, r, addresses[0], p)
		return
	}
	var conn net.Conn
	for _, ip := range addresses {
		conn, e = (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if e == nil {
			break
		}
	}
	if e != nil {
		status = 502
		if errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
			status = 504
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	defer conn.Close()
	w.WriteHeader(200)
	http.NewResponseController(w).Flush()
	cleanup := joinCancellation(r.Context(), func() {
		conn.Close()
		if permit(r.Context()) != nil {
			r.Body.Close()
			http.NewResponseController(w).SetWriteDeadline(time.Now())
		}
	})
	defer cleanup()
	idle := time.Duration(s.Config.IdleTimeout) * time.Second
	up := make(chan error, 1)
	go func() {
		var reader io.Reader = r.Body
		if p := permit(r.Context()); p != nil {
			reader = accountReader{reader: reader, access: p, upload: true}
		}
		_, e := io.CopyBuffer(idleConn{Conn: conn, idle: idle}, reader, make([]byte, 32<<10))
		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.CloseWrite()
		}
		up <- e
	}()
	var reader io.Reader = idleConn{Conn: conn, idle: idle}
	if p := permit(r.Context()); p != nil {
		reader = accountReader{reader: reader, access: p}
	}
	_, downErr := io.CopyBuffer(flushWriter{w: w}, reader, make([]byte, 32<<10))
	if downErr != nil {
		conn.Close()
		r.Body.Close()
	}
	select {
	case <-up:
	case <-r.Context().Done():
	}
}
func UDPAddress(u *url.URL) (string, error) {
	p := strings.Split(u.EscapedPath(), "/")
	if len(p) != 7 || p[1] != ".well-known" || p[2] != "masque" || p[3] != "udp" || p[6] != "" {
		return "", errors.New("bad UDP URI")
	}
	host, e := url.PathUnescape(p[4])
	if e != nil || host == "" || strings.ContainsAny(host, "[]/%\\ \t\r\n") {
		return "", errors.New("bad UDP host")
	}
	port, e := url.PathUnescape(p[5])
	if e != nil {
		return "", e
	}
	return net.JoinHostPort(host, port), nil
}
func (s *Server) resolve(ctx context.Context, host, target string) ([]net.IP, int) {
	s.TargetAttempts.Add(1)
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		var e error
		ips, e = s.Resolver(ctx, host)
		if e != nil {
			if ctx.Err() != nil {
				return nil, 504
			}
			return nil, 502
		}
	}
	allow := false
	for _, a := range s.Config.AllowedPrivateTargets {
		if a == target {
			allow = true
		}
	}
	result := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		a = a.Unmap()
		if !allow && (!a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()) {
			continue
		}
		result = append(result, ip)
	}
	if len(result) == 0 {
		return nil, 403
	}
	// Preferences reorder only addresses returned by the current DNS lookup and
	// already admitted by the target filter. They never add or pin stale addresses.
	preferred := s.Config.PreferredTargetIPs[strings.ToLower(strings.TrimSuffix(host, "."))]
	if len(preferred) != 0 {
		ordered := make([]net.IP, 0, len(result))
		used := make([]bool, len(result))
		for _, address := range preferred {
			ip := net.ParseIP(address)
			for i, candidate := range result {
				if !used[i] && candidate.Equal(ip) {
					ordered = append(ordered, candidate)
					used[i] = true
				}
			}
		}
		for i, candidate := range result {
			if !used[i] {
				ordered = append(ordered, candidate)
			}
		}
		result = ordered
	}
	return result, 0
}

type flushWriter struct{ w http.ResponseWriter }

func (w flushWriter) Write(p []byte) (int, error) {
	n, e := w.w.Write(p)
	if e == nil {
		e = http.NewResponseController(w.w).Flush()
	}
	return n, e
}

type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(b []byte) (int, error) {
	c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(b)
}
func (c idleConn) Write(b []byte) (int, error) {
	c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(b)
}

type pairRW struct {
	io.Reader
	io.Writer
}

func (s *Server) udp(w http.ResponseWriter, r *http.Request, ip net.IP, port int) {
	conn, e := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
	if e != nil {
		http.Error(w, "Bad Gateway", 502)
		return
	}
	defer conn.Close()
	w.Header().Set("Capsule-Protocol", "?1")
	w.WriteHeader(200)
	http.NewResponseController(w).Flush()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ps := wire.NewPacketStream(ctx, pairRW{Reader: r.Body, Writer: flushWriter{w}}, func() { r.Body.Close() })
	defer ps.Close()
	var once sync.Once
	closeAll := func() { once.Do(func() { cancel(); conn.Close(); ps.Close() }) }
	defer closeAll()
	stop := context.AfterFunc(ctx, closeAll)
	defer stop()
	if permit(r.Context()) != nil {
		stopWrite := joinCancellation(ctx, func() { http.NewResponseController(w).SetWriteDeadline(time.Now()) })
		defer stopWrite()
	}
	go func() {
		for {
			b, e := ps.Receive(ctx)
			if e != nil {
				closeAll()
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if p := permit(ctx); p != nil {
				if !p.Allowed() {
					closeAll()
					return
				}
				if charge(p, true, len(b)) != len(b) {
					closeAll()
					return
				}
			}
			if _, e = conn.Write(b); e != nil {
				closeAll()
				return
			}
		}
	}()
	b := make([]byte, 65535)
	for {
		conn.SetReadDeadline(time.Now().Add(time.Duration(s.Config.IdleTimeout) * time.Second))
		n, e := conn.Read(b)
		if e != nil {
			return
		}
		if p := permit(ctx); p != nil {
			if !p.Allowed() {
				return
			}
			if charge(p, false, n) != n {
				return
			}
		}
		if e = ps.Send(b[:n]); e != nil {
			return
		}
	}
}
func DefaultLookup(ctx context.Context, host string) ([]net.IP, error) {
	addrs, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	var ips []net.IP
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, e
}
func LogError(e error) {
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		slog.Error("server stopped", "error", fmt.Sprint(e))
		os.Exit(1)
	}
}
