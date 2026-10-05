package desktop

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"time"
	"tunnelx/internal/control"
)

// Control-plane bootstrap must work before the local tunnel exists, including
// when the launching process inherits a stale HTTP_PROXY/HTTPS_PROXY value.
// Keep normal certificate/hostname verification and never pin a fallback IP.
func newManagementClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 16, MaxIdleConnsPerHost: 4,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second, ExpectContinueTimeout: time.Second,
	}}
}

func managementErrorKind(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return "certificate"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return "connect"
	}
	return "network"
}

func managementErrorMessage(kind string) string {
	switch kind {
	case "dns":
		return "无法解析管理服务域名，请检查 DNS 或服务地址后重新加载。"
	case "timeout":
		return "连接管理服务超时，请点击重新加载；若持续失败，请在连接设置中导出诊断。"
	case "certificate":
		return "管理服务证书验证失败，请检查电脑时间或联系管理员；客户端未跳过证书验证。"
	case "connect":
		return "无法建立管理服务连接，请检查服务地址、网络或防火墙后重新加载。"
	default:
		return "管理服务连接中断，请重新加载；若持续失败，请在连接设置中导出诊断。"
	}
}

type managementEvent struct {
	Time      string `json:"time"`
	Operation string `json:"operation"`
	Method    string `json:"method"`
	Attempt   int    `json:"attempt"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Result    string `json:"result"`
	Status    int    `json:"http_status,omitempty"`
}

// Do not log raw errors, URLs, headers, identities or response bodies. The
// small rolling snapshot is available even before login or tunnel startup.
func (e *Engine) recordManagement(path, method string, attempt int, started time.Time, result string, status int) {
	op := "account"
	switch path {
	case "/api/public/settings":
		op = "public_settings"
	case "/api/public/commerce":
		op = "public_commerce"
	case "/api/login":
		op = "login"
	case "/api/nodes":
		op = "nodes"
	}
	event := managementEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), Operation: op, Method: method, Attempt: attempt, ElapsedMS: time.Since(started).Milliseconds(), Result: result, Status: status}
	e.managementMu.Lock()
	defer e.managementMu.Unlock()
	e.managementEvents = append(e.managementEvents, event)
	if len(e.managementEvents) > 64 {
		e.managementEvents = append([]managementEvent(nil), e.managementEvents[len(e.managementEvents)-64:]...)
	}
	b, err := json.MarshalIndent(e.managementEvents, "", "  ")
	if err == nil {
		err = control.WriteFile(filepath.Join(e.root, "state", "management-requests.json"), b)
	}
	e.managementLogWriteFailed = err != nil
}

func (e *Engine) managementReport() map[string]any {
	e.managementMu.Lock()
	defer e.managementMu.Unlock()
	return map[string]any{"route": "direct", "events": append([]managementEvent{}, e.managementEvents...), "log_write_failed": e.managementLogWriteFailed}
}
