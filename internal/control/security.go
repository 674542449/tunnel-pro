package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func validateCommercialConfig(c *Config) error {
	x := c.Commercial
	if x.PaymentMode != "" && x.PaymentMode != "disabled" && x.PaymentMode != "test" && x.PaymentMode != "stripe" && x.PaymentMode != "gateways" {
		return errors.New("unsupported payment mode")
	}
	if x.SecurityKey != "" {
		b, e := base64.StdEncoding.DecodeString(x.SecurityKey)
		if e != nil || len(b) != 32 {
			return errors.New("security key must contain 32 base64 bytes")
		}
	}
	if (x.Enabled || x.RequireAdminMFA || c.Mail.Mode == "test" || c.Mail.Mode == "smtp") && x.SecurityKey == "" {
		return errors.New("commercial mode, required MFA and mail delivery require a private security key")
	}
	if c.Mail.Mode != "" && c.Mail.Mode != "disabled" && c.Mail.Mode != "test" && c.Mail.Mode != "smtp" {
		return errors.New("invalid mail mode")
	}
	if c.Mail.Mode == "smtp" {
		if c.Mail.Host == "" || net.ParseIP(c.Mail.Host) != nil && net.ParseIP(c.Mail.Host).IsLoopback() || c.Mail.Port < 1 || c.Mail.Port > 65535 || !validEmail(c.Mail.From) || strings.ContainsAny(c.Mail.Host+c.Mail.From, "\r\n") {
			return errors.New("SMTP requires a valid host, port and sender")
		}
	}
	if c.Mail.AlertsTo != "" && !validEmail(c.Mail.AlertsTo) {
		return errors.New("invalid operations alert email")
	}
	providerConfigured := x.PaymentMode == "gateways" || x.PaymentMode == "stripe" && x.PaymentSecret != "" && x.WebhookSecret != ""
	if x.LiveApproved && (!providerConfigured || c.Mail.Mode != "smtp" || !x.RequireAdminMFA || c.DatabaseURL == "" || x.Terms == "" || x.Privacy == "" || x.RefundPolicy == "") {
		return errors.New("live commerce prerequisites incomplete")
	}
	return nil
}

var errMFAInvalid = errors.New("MFA invalid")

func (a *API) seal(purpose, value string) (string, error) {
	key, e := base64.StdEncoding.DecodeString(a.Config.Commercial.SecurityKey)
	if e != nil || len(key) != 32 {
		return "", errors.New("private security key unavailable")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	iv := make([]byte, g.NonceSize())
	if _, e = rand.Read(iv); e != nil {
		return "", e
	}
	raw := g.Seal(iv, iv, []byte(value), []byte(purpose))
	return base64.RawStdEncoding.EncodeToString(raw), nil
}
func (a *API) unseal(purpose, value string) (string, error) {
	key, e := base64.StdEncoding.DecodeString(a.Config.Commercial.SecurityKey)
	if e != nil || len(key) != 32 {
		return "", errors.New("private security key unavailable")
	}
	raw, e := base64.RawStdEncoding.DecodeString(value)
	if e != nil {
		return "", e
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil || len(raw) < g.NonceSize() {
		return "", errors.New("invalid protected value")
	}
	out, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(purpose))
	return string(out), e
}
func totp(secret string, step int64) (string, error) {
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if e != nil || len(key) < 16 {
		return "", errors.New("invalid MFA secret")
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	h := hmac.New(sha1.New, key)
	h.Write(b[:])
	digest := h.Sum(nil)
	offset := digest[len(digest)-1] & 15
	n := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000), nil
}
func (a *API) verifyMFA(u *User, code string, now int64) bool {
	if u.MFASecret == "" {
		return false
	}
	secret, e := a.unseal("mfa", u.MFASecret)
	if e != nil {
		return false
	}
	code = strings.TrimSpace(code)
	if len(code) == 6 {
		for _, offset := range []int64{0, -1, 1} {
			step := now/30 + offset
			if step <= u.MFALastStep {
				continue
			}
			expected, e := totp(secret, step)
			if e == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
				u.MFALastStep = step
				return true
			}
		}
	}
	if len(code) >= 16 {
		hash := Hash(code)
		list := strings.Split(u.RecoveryHashes, ",")
		for i, h := range list {
			if subtle.ConstantTimeCompare([]byte(hash), []byte(h)) == 1 {
				list = append(list[:i], list[i+1:]...)
				u.RecoveryHashes = strings.Join(list, ",")
				return true
			}
		}
	}
	return false
}
func revokeSessions(d *State, userID, except string) {
	keep := d.Sessions[:0]
	for _, s := range d.Sessions {
		if s.UserID != userID || s.Hash == except {
			keep = append(keep, s)
		}
	}
	d.Sessions = keep
}

// A password replacement also invalidates recovery links and unfinished MFA
// enrollments issued while the previous password was still valid.
func invalidateCredentialChallenges(d *State, userID string, now int64) {
	for i := range d.Challenges {
		g := &d.Challenges[i]
		if g.UserID == userID && (g.Purpose == "reset" || g.Purpose == "mfa") {
			if g.UsedAt == 0 {
				g.UsedAt = now
			}
			g.Secret = ""
		}
	}
}
func (a *API) challengeEmail(d *State, u User, purpose string, now int64) error {
	for i := range d.Challenges {
		g := &d.Challenges[i]
		if g.UserID == u.ID && g.Purpose == purpose && g.UsedAt == 0 {
			g.UsedAt = now
		}
	}
	token := Token()
	label := "验证邮箱"
	fragment := "verify"
	duration := int64(86400)
	if purpose == "reset" {
		label = "重置密码"
		fragment = "reset"
		duration = 1800
	}
	body := "tunnelX " + label + "\n\n" + a.Config.PublicURL + "/#" + fragment + "=" + url.QueryEscape(token) + "\n\n此链接仅可使用一次。若非本人操作，请忽略。\n"
	encrypted, e := a.seal("mail", body)
	if e != nil {
		return e
	}
	d.Challenges = append(d.Challenges, Challenge{ID: ID(), UserID: u.ID, Email: u.Email, Purpose: purpose, TokenHash: Hash(token), ExpiresAt: now + duration})
	d.Outbox = append(d.Outbox, MailMessage{ID: ID(), To: u.Email, Subject: "tunnelX · " + label, Body: encrypted, CreatedAt: now})
	return nil
}
func (a *API) publicSecurity(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/api/security/verify" && path != "/api/security/forgot" && path != "/api/security/reset" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return true
	}
	if !a.throttle(r) {
		fail(w, 429, "请求过于频繁")
		return true
	}
	var b struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return true
	}
	now := time.Now().Unix()
	if path == "/api/security/forgot" {
		if a.Config.Mail.Mode != "smtp" && a.Config.Mail.Mode != "test" {
			fail(w, 503, "密码找回尚未配置，请联系管理员")
			return true
		}
		email := strings.ToLower(strings.TrimSpace(b.Email))
		if !validEmail(email) {
			fail(w, 400, "邮箱格式无效")
			return true
		}
		e := a.Store.Update(func(d *State) error {
			for _, u := range d.Users {
				if u.Email == email && !u.Disabled {
					return a.challengeEmail(d, u, "reset", now)
				}
			}
			return nil
		})
		if e != nil {
			fail(w, 503, "暂时无法处理邮件请求")
			return true
		}
		reply(w, 202, map[string]bool{"accepted": true})
		return true
	}
	if len(b.Token) != 43 || path == "/api/security/reset" && (len(b.Password) < 12 || len(b.Password) > 256) {
		fail(w, 400, "链接或新密码无效")
		return true
	}
	password := ""
	if path == "/api/security/reset" {
		var e error
		password, e = passwordHash(b.Password)
		if e != nil {
			fail(w, 500, "密码处理失败")
			return true
		}
	}
	e := a.Store.Update(func(d *State) error {
		purpose := "verify"
		if password != "" {
			purpose = "reset"
		}
		for i := range d.Challenges {
			g := &d.Challenges[i]
			if g.Purpose == purpose && g.UsedAt == 0 && g.ExpiresAt > now && subtle.ConstantTimeCompare([]byte(g.TokenHash), []byte(Hash(b.Token))) == 1 {
				u := findUser(d, g.UserID)
				if u == nil || u.Disabled {
					return errors.New("invalid account")
				}
				g.UsedAt = now
				if purpose == "verify" {
					u.EmailVerifiedAt = now
				} else {
					u.PasswordHash = password
					u.TunnelToken = Token()
					revokeSessions(d, u.ID, "")
					invalidateCredentialChallenges(d, u.ID, now)
				}
				record(d, u.ID, "security_"+purpose, u.ID)
				return nil
			}
		}
		return errors.New("invalid challenge")
	})
	if e != nil {
		fail(w, 400, "链接已失效、已使用或保存失败")
		return true
	}
	reply(w, 200, map[string]bool{"ok": true})
	return true
}
func (a *API) mfaRequired(u *User, s *Session) bool {
	return a.Config.Commercial.RequireAdminMFA && !s.MFA && (u.Role == "admin")
}

func (a *API) failMFA(w http.ResponseWriter, u *User) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"error":        "请先在账号安全中完成二次验证，再进入管理页面",
		"mfa_required": true,
		"mfa_enabled":  u.MFASecret != "",
		"security_url": a.Config.PublicURL + "/#security",
	})
}

func (a *API) security(w http.ResponseWriter, r *http.Request, u *User, s *Session) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/security/")
	if !strings.HasPrefix(r.URL.Path, "/api/security/") {
		return false
	}
	if path == "status" && r.Method == "GET" {
		reply(w, 200, map[string]any{"mfa_enabled": u.MFASecret != "", "mfa_session": s.MFA, "mfa_required": a.mfaRequired(u, s), "mfa_enforced": a.Config.Commercial.RequireAdminMFA && (u.Role == "admin"), "email_verified": u.EmailVerifiedAt > 0, "verification_required": u.NeedsEmailVerification && u.EmailVerifiedAt == 0})
		return true
	}
	if r.Method != "POST" {
		fail(w, 404, "接口不存在")
		return true
	}
	if !a.throttle(r) {
		fail(w, 429, "安全操作过于频繁")
		return true
	}
	if path == "resend" {
		if a.Config.Mail.Mode != "smtp" && a.Config.Mail.Mode != "test" {
			fail(w, 503, "邮件尚未配置")
			return true
		}
		e := a.commit(u, s, func(d *State) error { return a.challengeEmail(d, *findUser(d, u.ID), "verify", time.Now().Unix()) })
		if e != nil {
			failCommit(w, e, 503, "无法保存邮件请求")
			return true
		}
		reply(w, 202, map[string]bool{"accepted": true})
		return true
	}
	var b struct {
		Password        string `json:"password"`
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
		ChallengeID     string `json:"challenge_id"`
	}
	if !decode(w, r, &b) {
		return true
	}
	if path == "password" {
		if len(b.Password) < 12 || len(b.Password) > 256 || !passwordValid(u.PasswordHash, b.CurrentPassword) {
			fail(w, 400, "原密码无效，或新密码不符合要求")
			return true
		}
		hash, e := passwordHash(b.Password)
		if e != nil {
			fail(w, 500, "密码处理失败")
			return true
		}
		e = a.commit(u, s, func(d *State) error {
			v := findUser(d, u.ID)
			if v.PasswordHash != u.PasswordHash {
				return errSessionRevoked
			}
			if v.MFASecret != "" && !a.verifyMFA(v, b.Code, time.Now().Unix()) {
				return errors.New("MFA required")
			}
			v.PasswordHash = hash
			v.TunnelToken = Token()
			revokeSessions(d, u.ID, "")
			invalidateCredentialChallenges(d, u.ID, time.Now().Unix())
			record(d, u.ID, "password_changed", u.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 400, "验证码无效或保存失败")
			return true
		}
		reply(w, 200, map[string]bool{"relogin": true})
		return true
	}
	if path == "mfa/begin" {
		if u.MFASecret != "" || !passwordValid(u.PasswordHash, b.Password) {
			fail(w, 400, "密码无效或已开启二次验证")
			return true
		}
		raw := make([]byte, 20)
		if _, e := rand.Read(raw); e != nil {
			fail(w, 500, "无法生成密钥")
			return true
		}
		secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		sealed, e := a.seal("mfa", secret)
		if e != nil {
			fail(w, 503, "后台安全密钥尚未配置")
			return true
		}
		challenge := Challenge{ID: ID(), UserID: u.ID, Purpose: "mfa", Secret: sealed, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
		e = a.commit(u, s, func(d *State) error {
			for i := range d.Challenges {
				if d.Challenges[i].UserID == u.ID && d.Challenges[i].Purpose == "mfa" {
					d.Challenges[i].UsedAt = time.Now().Unix()
				}
			}
			d.Challenges = append(d.Challenges, challenge)
			return nil
		})
		if e != nil {
			failCommit(w, e, 503, "无法保存验证请求")
			return true
		}
		uri := "otpauth://totp/" + url.PathEscape("tunnelX:"+u.Email) + "?secret=" + secret + "&issuer=tunnelX&algorithm=SHA1&digits=6&period=30"
		reply(w, 200, map[string]string{"challenge_id": challenge.ID, "secret": secret, "otpauth_uri": uri})
		return true
	}
	if path == "mfa/confirm" {
		codes := []string{}
		hashes := []string{}
		for i := 0; i < 10; i++ {
			code := Token()[:20]
			codes = append(codes, code)
			hashes = append(hashes, Hash(code))
		}
		e := a.commit(u, s, func(d *State) error {
			v := findUser(d, u.ID)
			if v.MFASecret != "" {
				return errors.New("already enrolled")
			}
			for i := range d.Challenges {
				g := &d.Challenges[i]
				if g.ID == b.ChallengeID && g.UserID == u.ID && g.Purpose == "mfa" && g.UsedAt == 0 && g.ExpiresAt > time.Now().Unix() {
					v.MFASecret = g.Secret
					if !a.verifyMFA(v, b.Code, time.Now().Unix()) {
						return errors.New("invalid code")
					}
					v.RecoveryHashes = strings.Join(hashes, ",")
					g.UsedAt = time.Now().Unix()
					g.Secret = ""
					revokeSessions(d, u.ID, s.Hash)
					for i := range d.Sessions {
						if d.Sessions[i].Hash == s.Hash {
							d.Sessions[i].MFA = true
						}
					}
					record(d, u.ID, "mfa_enabled", u.ID)
					return nil
				}
			}
			return errors.New("challenge expired")
		})
		if e != nil {
			failCommit(w, e, 400, "验证码无效、请求过期或保存失败")
			return true
		}
		reply(w, 200, map[string]any{"enabled": true, "recovery_codes": codes})
		return true
	}
	if path == "mfa/disable" {
		if a.Config.Commercial.RequireAdminMFA && (u.Role == "admin") {
			fail(w, 403, "当前要求工作账号二次验证，不能关闭")
			return true
		}
		if !passwordValid(u.PasswordHash, b.Password) {
			fail(w, 400, "密码无效")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			v := findUser(d, u.ID)
			if !a.verifyMFA(v, b.Code, time.Now().Unix()) {
				return errors.New("invalid MFA code")
			}
			v.MFASecret = ""
			v.RecoveryHashes = ""
			v.MFALastStep = 0
			revokeSessions(d, u.ID, "")
			record(d, u.ID, "mfa_disabled", u.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 400, "验证码无效或保存失败")
			return true
		}
		reply(w, 200, map[string]bool{"relogin": true})
		return true
	}
	fail(w, 404, "接口不存在")
	return true
}
