package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"tunnel/internal/mux"
	"tunnel/internal/proto"
	"tunnel/internal/relay"
	"tunnel/internal/socks5"

	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
)

type Config struct {
	LocalAddr string `json:"local_addr"`
	HTTPAddr  string `json:"http_addr"`
	RemoteURL string `json:"remote_url"`
	PSK       string `json:"psk"`
}

// Pool keeps one live mux connection, auto-reconnects on failure.
type Pool struct {
	mu   sync.Mutex
	conn *mux.Mux
	dial func() (*mux.Mux, error)
}

func (p *Pool) Get() (*mux.Mux, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil && !p.conn.IsClosed() {
		return p.conn, nil
	}

	m, err := p.dial()
	if err != nil {
		return nil, err
	}
	p.conn = m
	return m, nil
}

func main() {
	cfgPath := flag.String("c", "", "config file (optional)")
	flag.Parse()

	cfg := Config{
		LocalAddr: "127.0.0.1:1080",
		HTTPAddr:  "127.0.0.1:1081",
		RemoteURL: "wss://cloud.xiaguamail.com",
		PSK:       "F0vcOJsBa5iWb6JHipYo4WdrDndv14M",
	}
	if *cfgPath != "" {
		data, err := os.ReadFile(*cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Fatal(err)
		}
	}

	u, err := url.Parse(cfg.RemoteURL)
	if err != nil {
		log.Fatal(err)
	}
	serverHost := u.Hostname()

	serverPort := u.Port()
	if serverPort == "" {
		serverPort = "443"
	}
	serverIP := "129.146.200.250"

	wsDialer := &websocket.Dialer{
		Proxy:            func(*http.Request) (*url.URL, error) { return nil, nil },
		HandshakeTimeout: 15 * time.Second,
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			tcpConn, err := net.DialTimeout(network, serverIP+":"+serverPort, 15*time.Second)
			if err != nil {
				return nil, err
			}

			tlsCfg := &utls.Config{ServerName: serverHost}
			spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
			if err != nil {
				tcpConn.Close()
				return nil, err
			}
			for _, ext := range spec.Extensions {
				if alpn, ok := ext.(*utls.ALPNExtension); ok {
					alpn.AlpnProtocols = []string{"http/1.1"}
					break
				}
			}

			tlsConn := utls.UClient(tcpConn, tlsCfg, utls.HelloCustom)
			if err := tlsConn.ApplyPreset(&spec); err != nil {
				tcpConn.Close()
				return nil, err
			}
			if err := tlsConn.Handshake(); err != nil {
				tcpConn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}

	wsHeaders := http.Header{}
	wsHeaders.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	pool := &Pool{
		dial: func() (*mux.Mux, error) {
			log.Println("[pool] connecting...")
			dialURL := cfg.RemoteURL + proto.DynamicPath(cfg.PSK, 0)
			ws, _, err := wsDialer.Dial(dialURL, wsHeaders)
			if err != nil {
				return nil, fmt.Errorf("ws dial: %w", err)
			}
			m, err := mux.NewClientMux(ws, cfg.PSK)
			if err != nil {
				ws.Close()
				return nil, fmt.Errorf("mux auth: %w", err)
			}
			log.Println("[pool] mux ready")
			return m, nil
		},
	}

	openStream := func(host string, port uint16) (*mux.Stream, error) {
		for i := 0; i < 2; i++ {
			m, err := pool.Get()
			if err != nil {
				if i == 0 {
					continue
				}
				return nil, err
			}
			s, err := m.OpenStream(host, port)
			if err != nil {
				log.Printf("[tunnel] %s:%d retry: %v", host, port, err)
				continue
			}
			return s, nil
		}
		return nil, errors.New("tunnel unavailable")
	}

	// ---- SOCKS5 ----
	go func() {
		s := &socks5.Server{
			Addr: cfg.LocalAddr,
			OnConnect: func(conn net.Conn, host string, port uint16) {
				defer conn.Close()
				stream, err := openStream(host, port)
				if err != nil {
					log.Printf("[socks5] %s:%d %v", host, port, err)
					socks5.ReplyFailure(conn)
					return
				}
				defer stream.Close()
				socks5.ReplySuccess(conn)
				relay.Relay(stream, conn)
			},
		}
		if err := s.ListenAndServe(); err != nil {
			log.Fatalf("[socks5] %v", err)
		}
	}()

	// ---- HTTP proxy ----
	go func() {
		ln, err := net.Listen("tcp", cfg.HTTPAddr)
		if err != nil {
			log.Fatalf("[http] %v", err)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				continue
			}
			go handleHTTP(c, openStream)
		}
	}()

	// ---- system proxy ----
	setSystemProxy(cfg.HTTPAddr)
	defer clearSystemProxy()

	fmt.Println()
	fmt.Println("  ===== Tunnel 已启动 =====")
	fmt.Printf("  HTTP  代理: %s\n", cfg.HTTPAddr)
	fmt.Printf("  SOCKS5代理: %s\n", cfg.LocalAddr)
	fmt.Println("  系统代理:   已开启")
	fmt.Println("  按 Ctrl+C 退出")
	fmt.Println("  ========================")
	fmt.Println()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	fmt.Println()
	log.Println("正在关闭...")
}

func handleHTTP(conn net.Conn, openStream func(string, uint16) (*mux.Stream, error)) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		host, portStr, err := net.SplitHostPort(req.Host)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		port, _ := strconv.Atoi(portStr)

		stream, err := openStream(host, uint16(port))
		if err != nil {
			log.Printf("[http] CONNECT %s %v", req.Host, err)
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer stream.Close()
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		relay.Relay(stream, conn)
		return
	}

	host := req.URL.Hostname()
	port := uint16(80)
	if req.URL.Port() != "" {
		p, _ := strconv.Atoi(req.URL.Port())
		port = uint16(p)
	}

	stream, err := openStream(host, port)
	if err != nil {
		log.Printf("[http] %s %s %v", req.Method, req.Host, err)
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer stream.Close()

	req.RequestURI = req.URL.RequestURI()
	req.Header.Del("Proxy-Connection")
	var buf bytes.Buffer
	req.Write(&buf)
	stream.Write(buf.Bytes())
	relay.Relay(stream, conn)
}

func setSystemProxy(addr string) {
	regPath := `HKCU\Software\Microsoft\Windows\Internet Settings`
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", addr, "/f").Run()
	exec.Command("reg", "add", regPath, "/v", "ProxyOverride", "/t", "REG_SZ", "/d",
		"localhost;127.*;10.*;192.168.*;<local>", "/f").Run()
	refreshProxy()
}

func clearSystemProxy() {
	regPath := `HKCU\Software\Microsoft\Windows\Internet Settings`
	exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	refreshProxy()
	log.Println("系统代理已恢复")
}

func refreshProxy() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	set := wininet.NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0)
	set.Call(0, 37, 0, 0)
}
