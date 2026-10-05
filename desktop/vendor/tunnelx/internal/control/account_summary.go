package control

// Presentation uses current entitlement usage, not lifetime network counters.
// Authorization still uses accountForNode and preserves its node restrictions.
func accountPublic(d *State, u User, test bool, now int64) map[string]any {
	v := accountForNode(d, u, Node{TestOnly: test}, now)
	result := v.Public()
	total, used, remaining := u.Limit, u.Upload+u.Download, max(int64(0), u.Limit-u.Upload-u.Download)
	unlimited, exhausted := u.Limit == 0, v.QuotaExhausted
	expires, starts := u.ExpiresAt, int64(0)
	managed := u.Role != "admin" && entitled(d, u.ID, test) && (test || u.ExpiresAt <= now)
	current := v.ExpiresAt > now
	if managed {
		total, used, remaining, expires = 0, 0, 0, 0
		unlimited, current = false, false
		for _, g := range d.Entitlements {
			if g.UserID != u.ID || g.Test != test || g.RevokedAt != 0 || g.Kind == "traffic" {
				continue
			}
			expires = max(expires, g.EndsAt)
			if g.StartsAt > now && (starts == 0 || g.StartsAt < starts) {
				starts = g.StartsAt
			}
			if g.StartsAt <= now && g.EndsAt > now {
				current = true
			}
		}
		if current {
			for _, g := range d.Entitlements {
				if g.UserID != u.ID || g.Test != test || g.RevokedAt != 0 || g.StartsAt > now || g.EndsAt <= now {
					continue
				}
				used += g.Used
				if g.Bytes == 0 && g.Kind != "traffic" {
					unlimited = true
				} else {
					total += g.Bytes
					remaining += max(int64(0), g.Bytes-g.Used)
				}
			}
		}
		exhausted = current && !unlimited && remaining == 0
	} else {
		exhausted = current && !unlimited && used >= total
	}
	state := "not_subscribed"
	switch {
	case current:
		state = "valid"
	case starts > now:
		state = "scheduled"
	case expires > 0:
		state = "expired"
	}
	result["expires_at"] = expires
	result["subscription_status"] = state
	result["subscription_active"] = current
	result["traffic_exhausted"] = exhausted
	result["traffic"] = map[string]any{"total_bytes": total, "used_bytes": used, "remaining_bytes": remaining, "unlimited": unlimited && current, "exhausted": exhausted, "current_period": managed, "test": test}
	return result
}
