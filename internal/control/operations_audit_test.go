package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationsBackupHealthAndRecovery(t *testing.T) {
	for _, condition := range []string{"missing", "malformed", "expired", "failed", "future"} {
		t.Run(condition, func(t *testing.T) {
			a, s, admin := commercialSetup(t)
			a.Config.Mail.Mode = "disabled"
			a.Config.Mail.AlertsTo = "ops@example.test"
			now := time.Now().Unix()
			path := filepath.Join(filepath.Dir(a.Config.DataFile), "backup-status.json")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			var raw []byte
			switch condition {
			case "malformed":
				raw = []byte("not a valid status")
			case "expired":
				raw, _ = json.Marshal(map[string]int64{"time": now - 7200})
			case "failed":
				raw, _ = json.Marshal(map[string]int64{"time": now - 1, "failed_at": now - 1})
			case "future":
				raw, _ = json.Marshal(map[string]int64{"time": now + 3600})
			}
			if raw != nil {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := int64(0); i < 2; i++ {
				if err := a.maintain(now + i); err != nil {
					t.Fatal(err)
				}
			}
			d := a.Store.Snapshot()
			if len(d.Incidents) != 1 || d.Incidents[0].Key != "backup-unavailable" || d.Incidents[0].ResolvedAt != 0 {
				t.Fatal("backup failure did not create a single visible incident")
			}
			if len(d.Outbox) != 0 || d.Incidents[0].NotifiedAt != 0 {
				t.Fatal("unconfigured mail was treated as a sent notification")
			}
			if status := request(t, s, "admin/operations", admin, nil, 200); status["incident_count"] != float64(1) || status["mail_mode"] != "disabled" {
				t.Fatal("backup incident or mail mode was not visible in management operations")
			}
			a.Config.Mail.Mode = "test"
			if err := a.maintain(now + 2); err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(map[string]int64{"time": now + 3})
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			for i := int64(3); i < 5; i++ {
				if err := a.maintain(now + i); err != nil {
					t.Fatal(err)
				}
			}
			d = a.Store.Snapshot()
			if len(d.Incidents) != 1 || d.Incidents[0].ResolvedAt != now+3 || len(d.Outbox) != 2 {
				t.Fatal("successful backup did not resolve or deduplicate recovery notifications")
			}
			for _, m := range d.Outbox {
				if m.SentAt != 0 {
					t.Fatal("test-mode notification was marked as externally delivered")
				}
			}
		})
	}
}

func TestOperationsNoBackupAlarmForDisabledCommerce(t *testing.T) {
	a, _, _ := commercialSetup(t)
	a.Config.Commercial.Enabled = false
	if err := a.maintain(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if len(a.Store.Snapshot().Incidents) != 0 {
		t.Fatal("disabled commerce raised a backup alarm without a backup requirement")
	}
}

func TestOperationsTicketRejectsIgnoredStatus(t *testing.T) {
	a, s, admin := commercialSetup(t)
	request(t, s, "tickets", admin, map[string]string{"subject": "status input", "body": "should not be silently ignored", "status": "closed"}, 400)
	if len(a.Store.Snapshot().Tickets) != 0 {
		t.Fatal("unsupported status input created a ticket")
	}
	created := request(t, s, "tickets", admin, map[string]string{"subject": "supported close action", "body": "close through its action endpoint"}, 200)
	path := "tickets/" + created["ticket_id"].(string) + "/close"
	request(t, s, path, admin, map[string]string{"status": "closed"}, 400)
	if a.Store.Snapshot().Tickets[0].Status != "open" {
		t.Fatal("rejected status input changed a ticket")
	}
	request(t, s, path, admin, map[string]any{}, 200)
	if a.Store.Snapshot().Tickets[0].Status != "closed" {
		t.Fatal("supported close action did not close the ticket")
	}
}
