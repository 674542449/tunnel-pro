package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// Match profile export and node synchronization, including package node scope.
func userNode(d *State, u User, n Node, now int64) map[string]any {
	v := accountForNode(d, u, n, now)
	allowed := n.Enabled && !n.Pending() && v.Active(now) && !(v.NeedsEmailVerification && v.EmailVerifiedAt == 0)
	reason := ""
	if !allowed {
		switch {
		case !n.Enabled || n.Pending():
			reason = "节点未启用或尚未安装完成"
		case v.Disabled:
			reason = "账号已停用，请联系服务商"
		case v.NeedsEmailVerification && v.EmailVerifiedAt == 0:
			reason = "请先完成邮箱验证"
		case n.TestOnly && !u.Beta && u.Role != "admin":
			reason = "此线路仅供获邀测试账号使用"
		case v.QuotaExhausted || v.ExpiresAt > now && v.Limit > 0 && v.Upload+v.Download >= v.Limit:
			reason = "流量耗尽，订阅仍在有效期内，请加购流量或联系管理员"
		default:
			reason = "当前权益不可用于此线路，请检查套餐节点范围、到期时间和剩余额度"
		}
	}
	result := n.Public()
	result["access"] = map[string]any{"allowed": allowed, "reason": reason, "expires_at": v.ExpiresAt, "unlimited": v.Limit == 0 && !v.QuotaExhausted, "remaining_bytes": max(int64(0), v.Limit-v.Upload-v.Download), "device_limit": v.Devices}
	return result
}

func (a *API) profile(w http.ResponseWriter, r *http.Request, u *User, n *Node, bundle bool) {
	if n == nil || !n.Enabled || n.Pending() {
		fail(w, 404, "节点不存在或已停用")
		return
	}
	d := a.Store.Snapshot()
	projected := accountForNode(&d, *u, *n, time.Now().Unix())
	u = &projected
	if u.NeedsEmailVerification && u.EmailVerifiedAt == 0 {
		fail(w, 403, "请先验证邮箱")
		return
	}
	if !u.Active(time.Now().Unix()) {
		fail(w, 403, "账号未开通、已到期或流量已用完")
		return
	}
	c := n.Client
	c.Token = u.TunnelToken
	c.CAFile = "origin-ca.pem"
	c.DeviceID = ID()
	if !bundle {
		reply(w, 200, map[string]any{"node": n.Public(), "client": c, "ca_pem": n.CAPEM})
		return
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	cfg, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		fail(w, 500, "无法生成配置")
		return
	}
	files := []struct {
		name string
		data []byte
	}{
		{"client.json", append(cfg, '\n')}, {"origin-ca.pem", []byte(n.CAPEM)},
		{"README.txt", []byte("tunnelX 私有节点配置\n将 client.json 和 origin-ca.pem 放在同一目录。配置包含本账号的节点凭据，不要公开分享。\n")},
	}
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		h.SetMode(0600)
		out, err := z.CreateHeader(h)
		if err == nil {
			_, err = out.Write(f.data)
		}
		if err != nil {
			fail(w, 500, "无法生成配置包")
			return
		}
	}
	if e = z.Close(); e != nil {
		fail(w, 500, "无法生成配置包")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=tunnelX-node.zip")
	w.WriteHeader(200)
	_, _ = w.Write(b.Bytes())
}
