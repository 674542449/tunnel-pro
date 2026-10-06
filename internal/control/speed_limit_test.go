package control

import "testing"

func TestEntitlementSpeedLimitPrefersUnlimitedAndIgnoresAddOns(t *testing.T) {
	now := int64(1_800_000_000)
	u := User{ID: ID(), Role: "user", SpeedLimit: 999}
	sub := func(speed int64, kind string) Entitlement {
		return Entitlement{ID: ID(), UserID: u.ID, StartsAt: now - 10, EndsAt: now + 1000, Bytes: 1 << 30, Devices: 3, SpeedLimit: speed, Kind: kind}
	}
	for _, c := range []struct {
		name  string
		grant []Entitlement
		want  int64
	}{
		{"limited plan", []Entitlement{sub(1000, "")}, 1000},
		{"faster plan wins", []Entitlement{sub(1000, ""), sub(5000, "subscription")}, 5000},
		{"unlimited plan wins", []Entitlement{sub(1000, ""), sub(0, "")}, 0},
		{"add-on cannot throttle", []Entitlement{sub(0, ""), sub(1000, "traffic")}, 0},
	} {
		d := &State{Entitlements: c.grant, TestUsage: map[string]Counter{}}
		if got := accountForNode(d, u, Node{}, now).SpeedLimit; got != c.want {
			t.Errorf("%s: speed limit %d, want %d", c.name, got, c.want)
		}
	}
}
