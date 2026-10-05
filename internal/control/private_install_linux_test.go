//go:build linux

package control

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
)

// Explicit opt-in: installs only a fresh random-ID test node and removes its resources.
func TestPrivateNodeRealInstaller(t *testing.T) {
	if os.Getenv("TUNNELX_PRIVATE_INSTALL_ACCEPTANCE") != "1" {
		t.Skip("requires isolated root/systemd acceptance host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	a, s, admin := setup(t)
	a.Config.PublicURL = s.URL
	a.ArtifactDir = os.Getenv("TUNNELX_NODE_ARTIFACTS")
	if a.ArtifactDir == "" {
		t.Fatal("explicit artifact directory required")
	}
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", args[0], err, out)
		}
		return out
	}
	before := run("systemctl", "show", "tunnelx", "-p", "MainPID", "-p", "NRestarts")
	caddy, _ := os.ReadFile("/etc/caddy/Caddyfile")
	caddyHash := sha256.Sum256(caddy)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	data := request(t, s, "admin/nodes/setup", admin, map[string]any{"name": "private-install-acceptance", "ip": "203.0.113.20", "port": port, "certificate_mode": "private", "domain": "gateway-" + strings.Repeat("a", 50) + ".custom.invalid", "inner_name": "origin.custom.invalid"}, 200)
	id := data["node_id"].(string)
	unit := "tunnelx-node-" + id
	group := "txnode-" + id[:12]
	dir := "/etc/tunnelx-nodes/" + id
	bin := "/opt/tunnelx/nodes/" + id
	t.Cleanup(func() {
		exec.Command("systemctl", "disable", "--now", unit+".service", unit+"-cert.timer").Run()
		exec.Command("systemctl", "stop", unit+"-cert.service").Run()
		for _, suffix := range []string{".service", "-cert.service", "-cert.timer"} {
			os.Remove("/etc/systemd/system/" + unit + suffix)
		}
		exec.Command("systemctl", "daemon-reload").Run()
		for _, p := range []string{dir, bin, "/var/lib/tunnelx-nodes/" + id} {
			os.RemoveAll(p)
		}
		exec.Command("userdel", group).Run()
	})
	output := run("bash", "-c", data["command"].(string))
	if !strings.Contains(string(output), "节点已安装并对接后台") {
		t.Fatal("installer did not finish")
	}
	var n *Node
	for i := 0; i < 30; i++ {
		n = findNodePtr(a.Store.Snapshot(), id)
		if n.Enabled && !n.Pending() && n.LastSeen > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	if !n.Enabled || n.Pending() || n.LastSeen == 0 {
		t.Fatal("node did not report ready heartbeat")
	}
	profile := request(t, s, "nodes/"+id+"/profile", admin, nil, 200)
	raw, _ := json.Marshal(profile["client"])
	var cfg config.Client
	json.Unmarshal(raw, &cfg)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caPath, []byte(profile["ca_pem"].(string)), 0600)
	cfg.ServerIP = "127.0.0.1"
	cfg.CAFile = caPath
	cfg.ConnectTimeout = 2
	probe := func(c config.Client, ok bool) {
		t.Helper()
		m, e := client.New(c)
		if e != nil {
			t.Fatal(e)
		}
		defer m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, e := m.PublicGET(ctx)
		if res != nil {
			defer res.Body.Close()
		}
		if ok {
			if e != nil || res.StatusCode != 200 || !m.LastECH.Load() {
				t.Fatal("strict H2/ECH probe failed", e)
			}
		} else if e == nil {
			t.Fatal("invalid peer trusted")
		}
	}
	probe(cfg, true)
	// Exercise authenticated forwarding as well as the public TLS health endpoint.
	func() {
		m, err := client.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var stream *client.Tunnel
		for attempt := 0; attempt < 10; attempt++ {
			stream, err = m.OpenTCP(ctx, "example.com:80")
			if err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			t.Fatal("authenticated tunnel failed", err)
		}
		defer stream.Close()
		stream.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprint(stream, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
		response, err := http.ReadResponse(bufio.NewReader(stream), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Example Domain") {
			t.Fatal("tunnel download verification failed", err)
		}
	}()
	bad := cfg
	bad.CAFile = ""
	probe(bad, false)
	bad = cfg
	bad.ServerName = "wrong.custom.invalid"
	probe(bad, false)
	// Confirm private root cannot be read by the unprivileged node process.
	if exec.Command("runuser", "-u", group, "--", "test", "-r", dir+"/private-cert/ca/key.pem").Run() == nil {
		t.Fatal("CA key exposed to node service")
	}
	ca, _ := os.ReadFile(caPath)
	originalCA := sha256.Sum256(ca)
	pid := run("systemctl", "show", unit+".service", "-p", "MainPID")
	cert, _ := os.ReadFile(dir + "/private-cert/current/cert.pem")
	certHash := sha256.Sum256(cert)
	run("systemctl", "start", unit+"-cert.service")
	cert, _ = os.ReadFile(dir + "/private-cert/current/cert.pem")
	if sha256.Sum256(cert) != certHash {
		t.Fatal("healthy certificate unnecessarily rotated")
	}
	// Remove only this test node's leaf; renewal must retain the published CA and ECH keys.
	if err := os.Remove(dir + "/private-cert/current/cert.pem"); err != nil {
		t.Fatal(err)
	}
	run("systemctl", "start", unit+"-cert.service")
	cert, _ = os.ReadFile(dir + "/private-cert/current/cert.pem")
	if sha256.Sum256(cert) == certHash {
		t.Fatal("leaf was not renewed")
	}
	ca, _ = os.ReadFile(dir + "/origin-ca.pem")
	if sha256.Sum256(ca) != originalCA {
		t.Fatal("renewal changed CA")
	}
	probe(cfg, true)
	if string(pid) != string(run("systemctl", "show", unit+".service", "-p", "MainPID")) {
		t.Fatal("renewal restarted node")
	}
	if string(before) != string(run("systemctl", "show", "tunnelx", "-p", "MainPID", "-p", "NRestarts")) {
		t.Fatal("original service changed")
	}
	caddy, _ = os.ReadFile("/etc/caddy/Caddyfile")
	if sha256.Sum256(caddy) != caddyHash {
		t.Fatal("Caddy configuration changed")
	}
	fmt.Println("Private installer: custom names, no DNS/ACME, heartbeat, strict ECH/H2, wrong CA/name rejected, root-key permissions, stable-CA leaf renewal and unchanged original service: PASS")
}
