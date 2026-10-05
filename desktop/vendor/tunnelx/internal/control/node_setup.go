package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"tunnelx/internal/config"
)

//go:embed setup/*
var setupAssets embed.FS

type setupInput struct {
	Name            string `json:"name"`
	Region          string `json:"region"`
	IP              string `json:"ip"`
	Port            int    `json:"port"`
	Domain          string `json:"domain"`
	CertificateMode string `json:"certificate_mode"`
	InnerName       string `json:"inner_name"`
	TestOnly        bool   `json:"test_only"`
}
type setupArtifact struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validateSetup(b *setupInput) error {
	b.Name = strings.TrimSpace(b.Name)
	b.Region = strings.TrimSpace(b.Region)
	b.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(b.Domain), "."))
	b.InnerName = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(b.InnerName), "."))
	if b.CertificateMode == "" {
		b.CertificateMode = "public"
	}
	if b.CertificateMode != "public" && b.CertificateMode != "private" {
		return errors.New("证书模式无效")
	}
	if b.InnerName != "" && !validSetupName(b.InnerName) {
		return errors.New("内层证书名称格式无效")
	}
	ip := net.ParseIP(strings.TrimSpace(b.IP))
	if b.Name == "" || len(b.Name) > 128 || len(b.Region) > 32 || ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || b.Port < 1024 || b.Port > 65535 {
		return errors.New("需要节点名称、公网 IP 和 1024–65535 端口")
	}
	b.IP = ip.String()
	if b.CertificateMode == "private" {
		if b.Domain != "" && !validSetupName(b.Domain) {
			return errors.New("外层证书名称格式无效，请填写完整 DNS 名称")
		}
		return nil
	}
	if len(b.Domain) > 253 || net.ParseIP(b.Domain) != nil || !strings.Contains(b.Domain, ".") {
		return errors.New("需要已指向节点 IP 的域名")
	}
	for _, label := range strings.Split(b.Domain, ".") {
		if !dnsLabel.MatchString(label) {
			return errors.New("域名格式无效")
		}
	}
	for _, suffix := range []string{".invalid", ".local", ".localhost", ".test"} {
		if strings.HasSuffix(b.Domain, suffix) {
			return errors.New("需要可申请公开证书的域名")
		}
	}
	return nil
}
func validSetupName(name string) bool {
	if len(name) > 253 || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (a *API) script() []byte {
	b, _ := setupAssets.ReadFile("setup/install-node.sh")
	renew, _ := setupAssets.ReadFile("setup/renew-private-cert.py")
	return []byte(strings.Replace(string(b), "# TUNNELX_PRIVATE_CERT_IMPLEMENTATION", string(renew), 1))
}
func (a *API) installScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Write(a.script())
}
func (a *API) setupCommand(token string) string {
	scriptHash := sha256.Sum256(a.script())
	url := strings.TrimRight(a.Config.PublicURL, "/")
	program := "set -eu; test \"$(id -u)\" = 0 || { echo \"请使用 root 执行\" >&2; exit 1; }; umask 077; if ! command -v curl >/dev/null 2>&1; then apt-get update && apt-get install -y curl ca-certificates; fi; txdir=$(mktemp -d); trap 'rm -rf -- \"$txdir\"' EXIT; curl -fsS --proto '=https,http' --tlsv1.2 " + shQuote(url+"/install-node.sh") + " -o \"$txdir/install.sh\"; printf '%s  %s\\n' " + shQuote(hex.EncodeToString(scriptHash[:])) + " \"$txdir/install.sh\" | sha256sum -c -; bash \"$txdir/install.sh\" " + shQuote(url) + " " + shQuote(token)
	return "bash -c " + shQuote(program)
}
func (a *API) manifest(arch string) ([]setupArtifact, error) {
	if arch != "linux-arm64" && arch != "linux-amd64" {
		return nil, errors.New("unsupported architecture")
	}
	out := []setupArtifact{}
	for _, name := range []string{"tunnelx-server", "tunnelx-admin"} {
		f, e := os.Open(filepath.Join(a.ArtifactDir, arch, name))
		if e != nil {
			return nil, e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 32<<20 {
			f.Close()
			return nil, errors.New("invalid node binary")
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return nil, e
		}
		out = append(out, setupArtifact{Name: name, URL: strings.TrimRight(a.Config.PublicURL, "/") + "/api/node/setup/file/" + arch + "/" + name, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: info.Size()})
	}
	return out, nil
}
func (a *API) setupReady() error {
	for _, arch := range []string{"linux-arm64", "linux-amd64"} {
		if _, e := a.manifest(arch); e != nil {
			return e
		}
	}
	return nil
}
func (a *API) adminNodeSetup(w http.ResponseWriter, r *http.Request, actor *User, session *Session, path string, parts []string) bool {
	create := path == "nodes/setup"
	renew := len(parts) == 3 && parts[0] == "nodes" && parts[2] == "install"
	if !create && !renew {
		return false
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return true
	}
	if a.setupReady() != nil {
		fail(w, 503, "管理服务尚未安装节点内核文件，请检查 node-artifacts")
		return true
	}
	var input setupInput
	if create {
		if !decode(w, r, &input) {
			return true
		}
		if e := validateSetup(&input); e != nil {
			fail(w, 400, e.Error())
			return true
		}
	} else {
		var body struct{}
		if !decode(w, r, &body) {
			return true
		}
	}
	token := Token()
	expires := time.Now().Add(30 * time.Minute).Unix()
	id := ""
	e := a.commit(actor, session, func(d *State) error {
		var n *Node
		if create {
			if len(d.Nodes) >= 256 {
				return errors.New("node limit")
			}
			for _, old := range d.Nodes {
				if old.Client.ServerIP == input.IP && old.Client.Port == input.Port {
					return errors.New("node address already registered")
				}
			}
			id = ID()
			if input.InnerName == "" {
				input.InnerName = "edge-" + id + ".tunnelx.invalid"
			}
			if input.Domain == "" {
				input.Domain = "gateway-" + id + ".tunnelx.invalid"
			}
			d.Nodes = append(d.Nodes, Node{ID: id, Name: input.Name, Region: input.Region, AgentKey: Token(), Client: config.Client{ServerIP: input.IP, Port: input.Port, ServerName: input.InnerName, Transport: "h2", Privacy: "strict"}, Setup: &NodeSetup{Domain: input.Domain, CertificateMode: input.CertificateMode}})
			n = &d.Nodes[len(d.Nodes)-1]
			n.TestOnly = input.TestOnly || a.Config.Commercial.Enabled && a.paymentMode(d) == "test"
		} else {
			n = findNode(d, parts[1])
			if n == nil || !n.Pending() {
				return errors.New("node is not pending installation")
			}
			id = n.ID
			n.AgentKey = Token()
			n.LastSeen = 0
			n.Active = 0
		}
		n.Setup.TokenHash = Hash(token)
		n.Setup.ExpiresAt = expires
		n.Setup.Error = ""
		record(d, actor.ID, "node_install_command", id)
		return nil
	})
	if e != nil {
		failCommit(w, e, 409, "创建失败：相同 IP/端口可能已存在，或节点已完成安装")
		return true
	}
	mode := "public"
	snapshot := a.Store.Snapshot()
	if n := findNode(&snapshot, id); n != nil && n.Setup != nil && n.Setup.CertificateMode == "private" {
		mode = "private"
	}
	reply(w, 200, map[string]any{"node_id": id, "command": a.setupCommand(token), "expires_at": expires, "one_time": true, "certificate_mode": mode})
	return true
}
func setupToken(r *http.Request) string {
	h := r.Header.Values("Authorization")
	if len(h) != 1 || !strings.HasPrefix(h[0], "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(h[0], "Bearer ")
	if len(token) != 43 {
		return ""
	}
	return token
}
func setupNode(d *State, token string, completed bool) *Node {
	if token == "" {
		return nil
	}
	hash := Hash(token)
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.Setup != nil && n.Setup.ExpiresAt > time.Now().Unix() && subtle.ConstantTimeCompare([]byte(hash), []byte(n.Setup.TokenHash)) == 1 && (completed || n.Pending()) {
			return n
		}
	}
	return nil
}
func (a *API) nodeSetup(w http.ResponseWriter, r *http.Request) {
	token := setupToken(r)
	d := a.Store.Snapshot()
	completed := r.URL.Path == "/api/node/setup/complete" && r.Method == "POST"
	n := setupNode(&d, token, completed)
	if n == nil {
		fail(w, 403, "安装命令已失效、已使用或已被重新生成")
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/node/setup":
		files, e := a.manifest(r.URL.Query().Get("arch"))
		if e != nil {
			fail(w, 503, "节点架构不支持或内核文件不可用")
			return
		}
		reply(w, 200, map[string]any{"node_id": n.ID, "name": n.Name, "ip": n.Client.ServerIP, "port": n.Client.Port, "domain": n.Setup.Domain, "certificate_mode": n.Setup.CertificateMode, "inner_name": n.Client.ServerName, "expires_at": n.Setup.ExpiresAt, "artifacts": files, "agent": map[string]string{"api_url": a.Config.PublicURL, "node_id": n.ID, "agent_key": n.AgentKey, "state_file": "/var/lib/tunnelx-nodes/" + n.ID + "/agent-state.json"}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/node/setup/file/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/node/setup/file/"), "/")
		if len(parts) != 2 || (parts[0] != "linux-arm64" && parts[0] != "linux-amd64") || (parts[1] != "tunnelx-server" && parts[1] != "tunnelx-admin") {
			fail(w, 404, "文件不存在")
			return
		}
		f, e := os.Open(filepath.Join(a.ArtifactDir, parts[0], parts[1]))
		if e != nil {
			fail(w, 503, "内核文件不可用")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			fail(w, 503, "内核文件不可用")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, parts[1], info.ModTime(), f)
	case completed:
		var b struct {
			Client config.Client `json:"client"`
			CAPEM  string        `json:"ca_pem"`
		}
		if !decode(w, r, &b) {
			return
		}
		if !n.Pending() {
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if b.Client.ServerIP != n.Client.ServerIP || b.Client.Port != n.Client.Port || b.Client.ServerName != n.Client.ServerName {
			fail(w, 400, "安装参数与登记节点不一致")
			return
		}
		checked := Node{Name: n.Name, Region: n.Region, Client: b.Client, CAPEM: b.CAPEM}
		if e := validateNode(&checked); e != nil {
			fail(w, 400, e.Error())
			return
		}
		e := a.Store.Update(func(d *State) error {
			node := setupNode(d, token, true)
			if node == nil {
				return errors.New("installation revoked")
			}
			if !node.Pending() {
				return nil
			}
			node.Client = checked.Client
			node.CAPEM = checked.CAPEM
			node.Enabled = true
			node.Setup.CompletedAt = time.Now().Unix()
			node.Setup.Error = ""
			record(d, node.ID, "node_installed", node.ID)
			return nil
		})
		if e != nil {
			fail(w, 409, "安装凭据已撤销或无法保存")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case r.Method == "POST" && r.URL.Path == "/api/node/setup/error":
		var b struct {
			Phase string `json:"phase"`
		}
		if !decode(w, r, &b) {
			return
		}
		phases := map[string]string{"dependencies": "安装依赖", "download": "下载内核", "config": "生成配置", "certificate": "申请或同步证书", "service": "启动节点服务", "complete": "回报后台"}
		label, ok := phases[b.Phase]
		if !ok {
			fail(w, 400, "安装阶段无效")
			return
		}
		e := a.Store.Update(func(d *State) error {
			node := setupNode(d, token, false)
			if node == nil {
				return errors.New("installation revoked")
			}
			node.Setup.Error = label + "失败，请查看服务器输出后重新执行命令"
			return nil
		})
		if e != nil {
			fail(w, 409, "无法保存安装状态")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "接口不存在")
	}
}
