package control

import (
	"errors"
	"net/http"
	"time"
)

var errSessionRevoked = errors.New("session revoked")
var errTrialDisabled = errors.New("trial disabled")

func failCommit(w http.ResponseWriter, err error, status int, message string) {
	if errors.Is(err, errSessionRevoked) {
		fail(w, 401, "登录已失效，请重新登录")
		return
	}
	if errors.Is(err, errTrialDisabled) {
		fail(w, 403, "试用已关闭")
		return
	}
	if errors.Is(err, errTrafficBaseRequired) || errors.Is(err, errPlanNodes) || errors.Is(err, errNodeReferenced) || errors.Is(err, errBetaInvite) {
		fail(w, status, err.Error())
		return
	}
	fail(w, status, message)
}

// Recheck identity in the same transaction as the write, including revoked sessions.
func (a *API) commit(u *User, s *Session, fn func(*State) error) error {
	return a.Store.Update(func(d *State) error {
		current := findUser(d, u.ID)
		if current == nil || current.Disabled || current.Role != u.Role {
			return errSessionRevoked
		}
		found := false
		for _, v := range d.Sessions {
			if v.Hash == s.Hash && v.UserID == u.ID && v.ExpiresAt > time.Now().Unix() {
				found = true
				break
			}
		}
		if !found {
			return errSessionRevoked
		}
		return fn(d)
	})
}
func cancelOrder(d *State, id string, actor *User) error {
	for i := range d.Orders {
		o := &d.Orders[i]
		if o.ID != id {
			continue
		}
		if actor.Role != "admin" && o.UserID != actor.ID {
			return errors.New("order not owned")
		}
		if o.Status == "cancelled" {
			return nil
		}
		if o.Status != "pending" {
			return errors.New("order already fulfilled")
		}
		o.Status = "cancelled"
		record(d, actor.ID, "order_cancelled", o.ID)
		return nil
	}
	return errors.New("missing order")
}
