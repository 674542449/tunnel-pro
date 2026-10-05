package control

import (
	"encoding/base64"
	"testing"
)

func TestAuditEnabledSecurityFeaturesRequireUsableKey(t *testing.T) {
	for _, name := range []string{"commercial", "mfa", "test_mail", "smtp_mail"} {
		t.Run(name, func(t *testing.T) {
			c := Config{}
			switch name {
			case "commercial":
				c.Commercial.Enabled = true
			case "mfa":
				c.Commercial.RequireAdminMFA = true
			case "test_mail":
				c.Mail.Mode = "test"
			case "smtp_mail":
				c.Mail.Mode = "smtp"
				c.Mail.Host = "mail.example.test"
				c.Mail.Port = 587
				c.Mail.From = "test@example.test"
			}
			if validateCommercialConfig(&c) == nil {
				t.Fatal("enabled security feature starts without its encryption key")
			}
			c.Commercial.SecurityKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			if err := validateCommercialConfig(&c); err != nil {
				t.Fatal(err)
			}
		})
	}
	c := Config{}
	if err := validateCommercialConfig(&c); err != nil {
		t.Fatal("legacy noncommercial configuration must still work", err)
	}
}

func TestAuditCommercialTrialSettingIsNotSilentlyIgnored(t *testing.T) {
	a, s, admin := commercialSetup(t)
	before := a.accessSettings(stateSnapshot(a))
	request(t, s, "admin/settings", admin, map[string]any{"access": map[string]any{"registration": false, "trial_hours": 24}}, 400)
	if got := a.accessSettings(stateSnapshot(a)); got != before {
		t.Fatal("rejected setting changed access")
	}
	request(t, s, "admin/settings", admin, map[string]any{"access": map[string]any{"registration": false, "trial_hours": 0}}, 200)
	if got := a.accessSettings(stateSnapshot(a)); got.Registration || got.TrialHours != 0 {
		t.Fatal("valid registration setting did not take effect")
	}
}

func stateSnapshot(a *API) *State { d := a.Store.Snapshot(); return &d }
