package control

import (
	"errors"
	"strings"
	"time"
)

// ResetAdmin is the local, root-only recovery path for a locked-out
// administrator. It also clears MFA, because a lost second factor is the
// usual reason for being locked out, and revokes every existing session.
func (s *Store) ResetAdmin(current, email, password string) error {
	current = strings.ToLower(strings.TrimSpace(current))
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		email = current
	}
	if !validEmail(email) || len(password) < 12 || len(password) > 256 {
		return errors.New("邮箱无效，密码需要 12 至 256 字节")
	}
	hash, e := passwordHash(password)
	if e != nil {
		return e
	}
	return s.Update(func(d *State) error {
		var target *User
		for i := range d.Users {
			switch d.Users[i].Email {
			case current:
				target = &d.Users[i]
			case email:
				return errors.New("新邮箱已被其他账号使用")
			}
		}
		if target == nil || target.Role != "admin" {
			return errors.New("未找到该管理员账号")
		}
		target.Email, target.PasswordHash, target.TunnelToken = email, hash, Token()
		target.MFASecret, target.MFALastStep, target.RecoveryHashes = "", 0, ""
		invalidateCredentialChallenges(d, target.ID, time.Now().Unix())
		sessions := d.Sessions[:0]
		for _, v := range d.Sessions {
			if v.UserID != target.ID {
				sessions = append(sessions, v)
			}
		}
		d.Sessions = sessions
		record(d, target.ID, "password_reset", target.ID)
		return nil
	})
}
