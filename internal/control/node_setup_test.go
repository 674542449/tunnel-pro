package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"tunnelx/internal/pki"
)

func setupFiles(t *testing.T, a *API) {
	t.Helper()
	a.ArtifactDir = testTempDir(t)
	for _, arch := range []string{"linux-arm64", "linux-amd64"} {
		dir := filepath.Join(a.ArtifactDir, arch)
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"tunnelx-server", "tunnelx-admin"} {
			if e := os.WriteFile(filepath.Join(dir, name), []byte(arch+name), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func installNode(t *testing.T, a *API, s *httptest.Server, admin string) (string, string) {
	t.Helper()
	setupFiles(t, a)
	data := request(t, s, "admin/nodes/setup", admin, map[string]any{"name": "install-test", "region": "HK", "ip": "146.56.99.255", "port": 18447, "domain": "node.example.com"}, 200)
	matches := regexp.MustCompile(`[A-Za-z0-9_-]{43}`).FindAllString(data["command"].(string), -1)
	token := matches[len(matches)-1]
	return data["node_id"].(string), token
}
func TestNodeInstallValidationAndArtifactReadiness(t *testing.T) {
	a, s, admin := setup(t)
	// Do not discover real sidecars beside a deployed acceptance-test binary.
	a.ArtifactDir = testTempDir(t)
	request(t, s, "admin/nodes/setup", admin, map[string]any{}, 503)
	setupFiles(t, a)
	for _, bad := range []setupInput{{Name: "n", IP: "127.0.0.1", Port: 8443, Domain: "a.example.com"}, {Name: "n", IP: "10.1.1.1", Port: 8443, Domain: "a.example.com"}, {Name: "n", IP: "146.56.99.255", Port: 443, Domain: "a.example.com"}, {Name: "n", IP: "146.56.99.255", Port: 8443, Domain: "a.example.com;touch x"}, {Name: "n", IP: "146.56.99.255", Port: 8443, Domain: "a.invalid"}, {Name: "n", IP: "146.56.99.255", Port: 8443, Domain: "146.56.99.255"}} {
		request(t, s, "admin/nodes/setup", admin, bad, 400)
	}
	input := setupInput{Name: " Name ", IP: "146.56.99.255", Port: 8443, Domain: "NODE.Example.COM."}
	if e := validateSetup(&input); e != nil || input.Domain != "node.example.com" {
		t.Fatal(input, e)
	}
	member := request(t, s, "register", "", map[string]string{"email": "member@example.com", "password": "member-password-for-install"}, 200)["token"].(string)
	request(t, s, "admin/nodes/setup", member, input, 403)
}
func TestPrivateNodeSetupNamesAndPersistence(t *testing.T) {
	a, s, admin := setup(t)
	setupFiles(t, a)
	request(t, s, "admin/nodes/setup", admin, map[string]any{"name": "public", "ip": "203.0.113.20", "port": 18449, "domain": "node.example.com", "inner_name": "NODE.EXAMPLE.COM."}, 400)
	for _, field := range []string{"domain", "inner_name"} {
		for _, bad := range []string{"x;touch /tmp/bad", "*.example.com", "https://example.com", "127.0.0.1", "a..invalid", "-bad.invalid", "单独.测试"} {
			body := map[string]any{"name": "private", "ip": "203.0.113.20", "port": 18449, "certificate_mode": "private"}
			body[field] = bad
			request(t, s, "admin/nodes/setup", admin, body, 400)
		}
	}
	request(t, s, "admin/nodes/setup", admin, map[string]any{"name": "private", "ip": "203.0.113.20", "port": 18449, "certificate_mode": "skip_verify"}, 400)
	for i, custom := range []bool{false, true} {
		body := map[string]any{"name": "private", "ip": "203.0.113.20", "port": 18449 + i, "certificate_mode": "private"}
		if custom {
			body["domain"] = "Gateway.Custom.Invalid."
			body["inner_name"] = "Origin.Custom.Invalid."
		}
		result := request(t, s, "admin/nodes/setup", admin, body, 200)
		id := result["node_id"].(string)
		if result["certificate_mode"] != "private" {
			t.Fatal("mode not returned")
		}
		n := findNodePtr(a.Store.Snapshot(), id)
		wantOuter, wantInner := "gateway-"+id+".tunnelx.invalid", "edge-"+id+".tunnelx.invalid"
		if custom {
			wantOuter, wantInner = "gateway.custom.invalid", "origin.custom.invalid"
		}
		if n.Setup.Domain != wantOuter || n.Client.ServerName != wantInner {
			t.Fatal("certificate names not normalized")
		}
		matches := regexp.MustCompile(`[A-Za-z0-9_-]{43}`).FindAllString(result["command"].(string), -1)
		metadata := request(t, s, "node/setup?arch=linux-arm64", matches[len(matches)-1], nil, 200)
		if metadata["certificate_mode"] != "private" || metadata["inner_name"] != wantInner || metadata["domain"] != wantOuter {
			t.Fatal("installer metadata mismatch")
		}
		renewed := request(t, s, "admin/nodes/"+id+"/install", admin, map[string]any{}, 200)
		if renewed["certificate_mode"] != "private" {
			t.Fatal("regeneration lost certificate mode")
		}
		reopened, err := OpenStore(a.Config)
		if err != nil {
			t.Fatal(err)
		}
		if findNodePtr(reopened.Snapshot(), id).Setup.CertificateMode != "private" {
			t.Fatal("certificate mode not durable")
		}
		reopened.Close()
	}
}
func TestNodeInstallPendingIsolationAndCredentials(t *testing.T) {
	a, s, admin := setup(t)
	nodeID, token := installNode(t, a, s, admin)
	d := a.Store.Snapshot()
	n := findNode(&d, nodeID)
	if !n.Pending() || n.Enabled || n.Setup.TokenHash != Hash(token) {
		t.Fatal("pending registration incorrect")
	}
	raw, e := os.ReadFile(a.Config.DataFile)
	if e != nil || bytes.Contains(raw, []byte(token)) {
		t.Fatal("raw installation token persisted", e)
	}
	reopened, e := OpenStore(a.Config)
	if e != nil || !findNodePtr(reopened.Snapshot(), nodeID).Pending() {
		t.Fatal("pending node not durable", e)
	}
	b, _ := json.Marshal(n.Public())
	if bytes.Contains(b, []byte(n.AgentKey)) || bytes.Contains(b, []byte(n.Setup.TokenHash)) {
		t.Fatal("public secrets leaked")
	}
	request(t, s, "admin/nodes/"+nodeID+"/enabled", admin, map[string]bool{"enabled": true}, 409)
	request(t, s, "nodes/"+nodeID+"/profile", admin, nil, 404)
	req, _ := http.NewRequest("GET", s.URL+"/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []any
	json.NewDecoder(res.Body).Decode(&list)
	if res.StatusCode != 200 || len(list) != 0 {
		t.Fatal("pending node listed for client")
	}
	sync := map[string]any{"boot_id": ID(), "version": "v0.4.0", "active": 0, "counters": []any{}}
	response := setupSync(t, s, nodeID, n.AgentKey, sync, 200)
	if len(response["grants"].([]any)) != 0 {
		t.Fatal("pending grants disclosed")
	}
	bootstrap := request(t, s, "node/setup?arch=linux-arm64", token, nil, 200)
	if bootstrap["agent"].(map[string]any)["agent_key"] != n.AgentKey {
		t.Fatal("wrong agent identity")
	}
	files := bootstrap["artifacts"].([]any)
	f := files[0].(map[string]any)
	sum := sha256.Sum256([]byte("linux-arm64tunnelx-server"))
	if f["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatal("wrong artifact digest")
	}
	oldKey := n.AgentKey
	command := request(t, s, "admin/nodes/"+nodeID+"/install", admin, map[string]any{}, 200)["command"].(string)
	matches := regexp.MustCompile(`[A-Za-z0-9_-]{43}`).FindAllString(command, -1)
	newToken := matches[len(matches)-1]
	request(t, s, "node/setup?arch=linux-arm64", token, nil, 403)
	setupSync(t, s, nodeID, oldKey, sync, 403)
	request(t, s, "node/setup?arch=linux-amd64", newToken, nil, 200)
	a.Store.Update(func(d *State) error { findNode(d, nodeID).Setup.ExpiresAt = time.Now().Unix() - 1; return nil })
	request(t, s, "node/setup?arch=linux-arm64", newToken, nil, 403)
}
func findNodePtr(d State, id string) *Node { return findNode(&d, id) }
func setupSync(t *testing.T, s *httptest.Server, id, key string, body any, status int) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, _ := http.NewRequest("POST", s.URL+"/api/node/sync", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("X-TunnelX-Node", id)
	res, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("node sync status %d, want %d", res.StatusCode, status)
	}
	var result map[string]any
	json.NewDecoder(res.Body).Decode(&result)
	return result
}
func TestNodeInstallScopedDownloadsAndErrorReporting(t *testing.T) {
	a, s, admin := setup(t)
	nodeID, token := installNode(t, a, s, admin)
	request(t, s, "node/setup?arch=windows-amd64", token, nil, 503)
	request(t, s, "node/setup/file/linux-arm64/control.json", token, nil, 404)
	request(t, s, "node/setup/file/linux-amd64/tunnelx-server", Token(), nil, 403)
	r, _ := http.NewRequest("GET", s.URL+"/api/node/setup/file/linux-amd64/tunnelx-server", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	res, e := s.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(raw) != "linux-amd64tunnelx-server" {
		t.Fatal("artifact delivery failed")
	}
	request(t, s, "node/setup/error", token, map[string]string{"phase": "certificate"}, 200)
	n := findNodePtr(a.Store.Snapshot(), nodeID)
	if !strings.Contains(n.Setup.Error, "证书") {
		t.Fatal("phase error missing")
	}
	request(t, s, "node/setup/error", token, map[string]string{"phase": "private credential raw output"}, 400)
	request(t, s, "node/setup/error", token, map[string]string{"phase": "download", "detail": "private log"}, 400)
}
func TestNodeInstallCompletionAndIdempotentRetry(t *testing.T) {
	a, s, admin := setup(t)
	nodeID, token := installNode(t, a, s, admin)
	n := findNodePtr(a.Store.Snapshot(), nodeID)
	_, ech, e := pki.ECH(n.Setup.Domain)
	if e != nil {
		t.Fatal(e)
	}
	ca, _, key, e := pki.Certificate(n.Client.ServerName)
	if e != nil {
		t.Fatal(e)
	}
	c := n.Client
	c.ECHConfig = base64.StdEncoding.EncodeToString(ech)
	bad := c
	bad.Port++
	request(t, s, "node/setup/complete", token, map[string]any{"client": bad, "ca_pem": string(ca)}, 400)
	request(t, s, "node/setup/complete", token, map[string]any{"client": c, "ca_pem": string(ca) + string(key)}, 400)
	bad = c
	bad.ECHConfig = "YWJj"
	request(t, s, "node/setup/complete", token, map[string]any{"client": bad, "ca_pem": string(ca)}, 400)
	body := map[string]any{"client": c, "ca_pem": string(ca)}
	request(t, s, "node/setup/complete", token, body, 200)
	ready := findNodePtr(a.Store.Snapshot(), nodeID)
	if ready.Pending() || !ready.Enabled || ready.Client.Token != "" || ready.Setup.CompletedAt == 0 {
		t.Fatal("completion incorrect")
	}
	before := a.Store.Snapshot()
	request(t, s, "node/setup/complete", token, map[string]any{}, 200)
	after := a.Store.Snapshot()
	if len(after.Audit) != len(before.Audit) || findNodePtr(after, nodeID).Client.ECHConfig != ready.Client.ECHConfig {
		t.Fatal("duplicate completion mutated node")
	}
	request(t, s, "node/setup?arch=linux-arm64", token, nil, 403)
	request(t, s, "node/setup/error", token, map[string]string{"phase": "service"}, 403)
	request(t, s, "admin/nodes/"+nodeID+"/install", admin, map[string]any{}, 409)
	response := setupSync(t, s, nodeID, ready.AgentKey, map[string]any{"boot_id": ID(), "version": "v0.4.0", "counters": []any{}}, 200)
	if len(response["grants"].([]any)) == 0 {
		t.Fatal("ready grants missing")
	}
}
func TestNodeInstallScriptIsPublicAndCommandPinsDigest(t *testing.T) {
	a, _, _ := setup(t)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/install-node.sh", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "set -Eeuo pipefail") || bytes.Contains(w.Body.Bytes(), []byte{'\r'}) {
		t.Fatal("invalid embedded installer")
	}
	hash := sha256.Sum256(w.Body.Bytes())
	command := a.setupCommand(Token())
	if !strings.Contains(command, hex.EncodeToString(hash[:])) || !strings.Contains(command, "sha256sum -c -") {
		t.Fatal("script not pinned")
	}
}
