package desktop

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func ValidateURL(base string) error {
	_, err := NormalizeURL(base)
	return err
}

// Website links commonly end in a slash. Canonicalize before storing or
// comparing the base so copying one cannot invalidate a working session.
func NormalizeURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, e := url.Parse(base)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(base, "?#\\\x00\r\n") {
		return "", errors.New("服务地址应为不含账号密码、查询参数或锚点的 HTTPS 地址")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", errors.New("服务地址端口无效")
		}
	}
	if u.Scheme == "https" {
		return u.String(), nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return u.String(), nil
	}
	return "", errors.New("管理 API 必须使用 HTTPS")
}
