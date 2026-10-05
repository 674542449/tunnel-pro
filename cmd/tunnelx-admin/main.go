package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"tunnelx/internal/config"
	"tunnelx/internal/pki"
)

func main() {
	out := flag.String("out", ".local/deployment", "output directory (must not already contain server.json)")
	domain := flag.String("domain", "", "public certificate hostname")
	ip := flag.String("ip", "", "bootstrap IP")
	port := flag.Int("port", 8443, "TCP port")
	inner := flag.String("inner", "edge.tunnelx.invalid", "private ECH origin")
	flag.Parse()
	if *domain == "" || *ip == "" {
		fail(fmt.Errorf("domain and ip required"))
	}
	if _, e := os.Stat(filepath.Join(*out, "server.json")); e == nil {
		fail(fmt.Errorf("refusing to replace existing deployment secrets"))
	}
	if e := os.MkdirAll(*out, 0700); e != nil {
		fail(e)
	}
	token := make([]byte, 32)
	if _, e := rand.Read(token); e != nil {
		fail(e)
	}
	encoded := base64.RawURLEncoding.EncodeToString(token)
	ek, list, e := pki.ECH(*domain)
	if e != nil {
		fail(e)
	}
	ca, cert, key, e := pki.Certificate(*inner)
	if e != nil {
		fail(e)
	}
	write := func(name string, b []byte) {
		if e := os.WriteFile(filepath.Join(*out, name), b, 0600); e != nil {
			fail(e)
		}
	}
	jsonFile := func(name string, v any) {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			fail(e)
		}
		write(name, append(b, '\n'))
	}
	write("origin-ca.pem", ca)
	write("inner-cert.pem", cert)
	write("inner-key.pem", key)
	jsonFile("ech-key.json", ek)
	s := config.Server{Listen: fmt.Sprintf(":%d", *port), CertFile: "cert.pem", KeyFile: "key.pem", ECHKeyFile: "ech-key.json", InnerName: *inner, InnerCertFile: "inner-cert.pem", InnerKeyFile: "inner-key.pem", Tokens: []string{encoded}, MaxConnections: 256, DialTimeout: 10, IdleTimeout: 120}
	jsonFile("server.json", s)
	c := config.Client{ServerName: *domain, ServerIP: *ip, Port: *port, Token: encoded, Transport: "h2", Privacy: "normal", SOCKSListen: "127.0.0.1:1080", HTTPListen: "127.0.0.1:8088", WebListen: "127.0.0.1:9080", MaxConnections: 128, ConnectTimeout: 5}
	if e = c.Validate(); e != nil {
		fail(e)
	}
	jsonFile("client.json", c)
	c.ServerName = *inner
	c.Privacy = "strict"
	c.ECHConfig = base64.StdEncoding.EncodeToString(list)
	c.CAFile = "origin-ca.pem"
	jsonFile("client-strict.json", c)
	fmt.Println("Deployment files generated. Credentials were saved to private configuration files, not printed.")
}
func fail(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
