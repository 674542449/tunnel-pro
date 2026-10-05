package nodeagent

import (
	"path/filepath"
	"testing"
	"time"
	"tunnelx/internal/control"
)

func TestUncleanRestartQuarantinesUnreportedQuota(t *testing.T) {
	c := Config{APIURL: "http://127.0.0.1:1", NodeID: control.ID(), AgentKey: control.Token(), StateFile: filepath.Join(testTempDir(t), "state.json")}
	a, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	l := control.Lease{ID: control.ID(), NodeID: c.NodeID, UserID: control.ID(), DeviceID: control.ID(), Budget: 1024, Used: 100, ExpiresAt: time.Now().Unix() + 20}
	a.state.Leases["device"] = l
	if e = a.save(); e != nil {
		t.Fatal(e)
	}
	restarted, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	if len(restarted.state.Leases) != 0 || len(restarted.state.RecoveryLeases) != 1 || restarted.state.RecoveryLeases[0].ID != l.ID || restarted.state.RecoveryLeases[0].Budget != l.Budget {
		t.Fatal("unclean restart could release unreported capacity")
	}
	if restarted.state.Clean {
		t.Fatal("running state incorrectly marked clean")
	}
}
