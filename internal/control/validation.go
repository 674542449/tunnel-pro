package control

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"tunnelx/internal/config"
)

var releaseVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?(?:\+[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?$`)

var errPlanNodes = errors.New("套餐节点范围无效，请选择存在的节点，且不要重复选择")
var errNodeReferenced = errors.New("节点仍被套餐、未到期订单或有效权益引用，请先调整套餐、取消待付订单或处理相关退款")

func nodeReferenced(d *State, id string, now int64) bool {
	for _, p := range d.Plans {
		if slices.Contains(p.NodeIDs, id) {
			return true
		}
	}
	for _, o := range d.Orders {
		if o.Status == "pending" && (o.ExpiresAt == 0 || o.ExpiresAt > now) && slices.Contains(o.Plan.NodeIDs, id) {
			return true
		}
	}
	for _, g := range d.Entitlements {
		if g.RevokedAt == 0 && g.EndsAt > now && slices.Contains(g.NodeIDs, id) {
			return true
		}
	}
	return false
}

func validatePlanNodes(d *State, p Plan) error {
	if len(p.NodeIDs) > 256 {
		return errPlanNodes
	}
	seen := make(map[string]bool, len(p.NodeIDs))
	for _, id := range p.NodeIDs {
		if seen[id] || findNode(d, id) == nil {
			return errPlanNodes
		}
		seen[id] = true
	}
	return nil
}

func validateRelease(r *Release) error {
	r.Version = strings.TrimSpace(r.Version)
	r.URL = strings.TrimSpace(r.URL)
	r.SHA256 = strings.ToLower(strings.TrimSpace(r.SHA256))
	if len(r.Version) > 64 || !releaseVersion.MatchString(r.Version) || len(r.Notes) > 4000 || len(r.URL) > 2048 {
		return errors.New("版本或说明无效")
	}
	if r.URL == "" {
		if r.SHA256 != "" {
			return errors.New("清空下载地址时也需要清空 SHA256")
		}
		return nil
	}
	u, e := url.Parse(r.URL)
	sum, e2 := hex.DecodeString(r.SHA256)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || e2 != nil || len(sum) != 32 {
		return errors.New("更新需要 HTTPS 地址与 64 位十六进制 SHA256")
	}
	return nil
}

func validateNode(n *Node) error {
	n.Setup = nil
	n.Name = strings.TrimSpace(n.Name)
	n.Region = strings.TrimSpace(n.Region)
	n.Client.ServerName = strings.TrimSpace(n.Client.ServerName)
	n.Client.ServerIP = strings.TrimSpace(n.Client.ServerIP)
	n.Client.ECHConfig = strings.TrimSpace(n.Client.ECHConfig)
	if n.Name == "" || len(n.Name) > 128 || len(n.Region) > 32 {
		return errors.New("节点名称或地区无效")
	}
	n.Client.Token = Token()
	n.Client.Transport = "h2"
	n.Client.Privacy = "strict"
	n.Client.CAFile = "origin-ca.pem"
	n.Client.H2Connections = 4
	n.Client.H2ReadIdle = 5
	n.Client.H2PingTimeout = 2
	n.Client.H2MaxAge = 120
	n.Client.H2RetryNew = true
	if e := n.Client.Validate(); e != nil {
		return errors.New("节点配置无效：" + e.Error())
	}
	// Export only CA certificates, never an accidentally pasted certificate/key bundle.
	raw := bytes.TrimSpace([]byte(n.CAPEM))
	count := 0
	for len(raw) > 0 {
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return errors.New("origin CA 只能包含 CA 证书，不能包含私钥或其他内容")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return errors.New("origin CA PEM 无效")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		now := time.Now()
		if e != nil || !cert.IsCA || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return errors.New("origin CA 无效、尚未生效或已过期")
		}
		count++
		raw = bytes.TrimSpace(rest)
	}
	if count == 0 {
		return errors.New("需要 origin CA PEM")
	}
	if e := validateECH(n.Client); e != nil {
		return e
	}
	n.Client.Token = ""
	n.Client.DeviceID = ""
	n.Client.CAFile = ""
	n.AgentKey = ""
	n.ID = ""
	n.LastSeen = 0
	n.Active = 0
	n.Version = ""
	return nil
}

// Let Go TLS parse and select the ECH configuration while generating a ClientHello.
// This connection never performs DNS or network I/O and cannot transmit a packet.
var helloGenerated = errors.New("validation ClientHello generated")

type helloProbe struct{ handshake bool }

func (p *helloProbe) Read([]byte) (int, error) { return 0, io.EOF }
func (p *helloProbe) Write(b []byte) (int, error) {
	if len(b) > 0 && b[0] == 22 {
		p.handshake = true
	}
	return 0, helloGenerated
}
func (*helloProbe) Close() error                     { return nil }
func (*helloProbe) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*helloProbe) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*helloProbe) SetDeadline(time.Time) error      { return nil }
func (*helloProbe) SetReadDeadline(time.Time) error  { return nil }
func (*helloProbe) SetWriteDeadline(time.Time) error { return nil }
func validateECH(c config.Client) error {
	c.CAFile = ""
	tc, e := c.TLS()
	if e != nil {
		return errors.New("ECHConfigList 无效")
	}
	p := &helloProbe{}
	e = tls.Client(p, tc).Handshake()
	if !p.handshake || !errors.Is(e, helloGenerated) {
		return errors.New("ECHConfigList 格式错误或不受当前内核支持")
	}
	return nil
}
