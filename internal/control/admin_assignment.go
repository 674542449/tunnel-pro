package control

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

type assignmentRequest struct {
	PlanID    string `json:"plan_id"`
	Mode      string `json:"mode"`
	Test      bool   `json:"test"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
}

func assignPlan(d *State, actor, userID string, b assignmentRequest, now int64) error {
	key, err := hex.DecodeString(b.RequestID)
	if err != nil || len(key) != 16 || b.RequestID != strings.ToLower(b.RequestID) || len(strings.TrimSpace(b.Reason)) < 4 || len(b.Reason) > 1000 || (b.Mode != "immediate" && b.Mode != "renew") {
		return errors.New("请选择生效方式并填写分配原因")
	}
	for _, g := range d.Entitlements {
		if g.Source != "admin_assignment" || g.RequestID != b.RequestID {
			continue
		}
		if g.UserID != userID || g.AssignedBy != actor || g.PlanID != b.PlanID || g.Test != b.Test || g.AssignMode != b.Mode || g.Reason != b.Reason {
			return errors.New("重复请求的分配内容不一致，请刷新后核实")
		}
		return nil
	}
	u := findUser(d, userID)
	if u == nil || u.Role == "admin" || u.Disabled || b.Test && !u.Beta {
		return errors.New("此账号不可分配所选范围的套餐")
	}
	// Legacy grants bypass ledger node scopes; do not silently grant ignored rights.
	if !b.Test && u.ExpiresAt > now {
		return errors.New("账号仍有旧版授权，请待其到期后分配周期套餐")
	}
	var p Plan
	for _, plan := range d.Plans {
		if plan.ID == b.PlanID && plan.Enabled {
			p = plan
			break
		}
	}
	if p.ID == "" {
		return errors.New("套餐不存在或已停用")
	}
	if err := financialPlan(&p); err != nil {
		return err
	}
	o := Order{UserID: userID, Plan: p, Test: b.Test}
	if err := validatePurchase(d, o, now); err != nil {
		return err
	}
	if len(p.NodeIDs) > 0 {
		matching := false
		for _, id := range p.NodeIDs {
			if n := findNode(d, id); n != nil && n.TestOnly == b.Test {
				matching = true
			}
		}
		if !matching {
			return errors.New("套餐节点与所选正式 / 测试范围不匹配")
		}
	}
	start, end, devices := now, now+int64(p.Days)*86400, p.Devices
	if p.Kind == "traffic" {
		end, devices = trafficBaseEnd(d, o, now), 0
	} else if b.Mode == "renew" {
		for _, g := range d.Entitlements {
			if g.UserID == userID && g.Test == b.Test && g.RevokedAt == 0 && g.Kind != "traffic" {
				start = max(start, g.EndsAt)
			}
		}
		end = start + int64(p.Days)*86400
	}
	g := Entitlement{ID: ID(), UserID: userID, Source: "admin_assignment", PlanID: p.ID, PlanName: p.Name, AssignedBy: actor, Reason: b.Reason, RequestID: b.RequestID, AssignMode: b.Mode, StartsAt: start, EndsAt: end, Bytes: p.TrafficBytes, Devices: devices, SpeedLimit: p.SpeedLimit, NodeIDs: append([]string(nil), p.NodeIDs...), Test: b.Test, Kind: p.Kind}
	d.Entitlements = append(d.Entitlements, g)
	record(d, actor, "user_plan_assigned", g.ID)
	return nil
}

func (a *API) adminAssign(w http.ResponseWriter, r *http.Request, actor *User, session *Session, userID string) {
	if !a.Config.Commercial.Enabled {
		fail(w, 409, "请在商业套餐模式下分配套餐；旧版授权可使用续期")
		return
	}
	var b assignmentRequest
	if !decode(w, r, &b) {
		return
	}
	var validationErr error
	err := a.commit(actor, session, func(d *State) error {
		validationErr = assignPlan(d, actor.ID, userID, b, time.Now().Unix())
		return validationErr
	})
	if err != nil {
		if errors.Is(err, errSessionRevoked) {
			failCommit(w, err, 409, "")
		} else if validationErr != nil {
			fail(w, 409, validationErr.Error())
		} else {
			fail(w, 409, "分配未保存，请核实存储状态后重试")
		}
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
