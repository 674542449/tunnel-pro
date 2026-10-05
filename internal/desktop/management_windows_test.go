//go:build windows

package desktop

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagementBootstrapIgnoresInheritedProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"registration":true}`)) }))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	defer e.Close()
	tr := e.http.Transport.(*http.Transport)
	if tr.Proxy != nil || !tr.ForceAttemptHTTP2 || tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("bootstrap must be direct and verify TLS")
	}
	if _, err := e.Query("/api/public/settings", nil); err != nil {
		t.Fatal(err)
	}
}

func TestManagementRetriesReadButNeverMutationOrCertificate(t *testing.T) {
	for _, name := range []string{"read", "mutation", "certificate", "status"} {
		t.Run(name, func(t *testing.T) {
			e, _ := New(t.TempDir(), Settings{APIURL: "https://service.example.test"})
			calls := 0
			e.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if name == "certificate" {
					return nil, x509.UnknownAuthorityError{}
				}
				if name != "status" && calls == 1 {
					return nil, errors.New("connection lost with private-token")
				}
				status := 200
				if name == "status" {
					status = 503
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: r}, nil
			})
			var input any
			if name == "mutation" {
				input = map[string]string{"password": "private-password"}
			}
			var out any
			err := e.doRequest(context.Background(), "https://service.example.test", "/api/login", "private-token", input, &out)
			want := 1
			if name == "read" {
				want = 2
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("expected error")
			}
			if calls != want {
				t.Fatalf("calls = %d, want %d", calls, want)
			}
			report, _ := e.DiagnosticsReport()
			persisted, readErr := os.ReadFile(filepath.Join(e.root, "state", "management-requests.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, secret := range []string{"private-token", "private-password", "service.example.test"} {
				if strings.Contains(report+string(persisted), secret) || err != nil && strings.Contains(err.Error(), secret) {
					t.Fatal("diagnostics leaked private data")
				}
			}
		})
	}
}

func TestManagementRetryCancellationAndSafeCategories(t *testing.T) {
	e, _ := New(t.TempDir(), Settings{APIURL: "https://service.example.test"})
	calls := 0
	e.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.doRequest(ctx, "https://service.example.test", "/api/nodes", "", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("cancelled read retried")
	}
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{&net.DNSError{Err: "private domain", Name: "private.example"}, "dns"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "dial", Err: errors.New("private target")}, "connect"},
		{x509.UnknownAuthorityError{}, "certificate"},
	} {
		if managementErrorKind(tc.err) != tc.kind {
			t.Fatal(tc.kind)
		}
	}
}

func TestManagementRetainsBoundedRecordsAndRejectsUntrustedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted request reached handler") }))
	defer server.Close()
	e, _ := New(t.TempDir(), Settings{APIURL: server.URL})
	defer e.Close()
	if _, err := e.Query("/api/public/settings", nil); err == nil || !strings.Contains(err.Error(), "证书") {
		t.Fatal(err)
	}
	if len(e.managementEvents) != 1 || e.managementEvents[0].Result != "certificate" {
		t.Fatal("TLS failure retried or misclassified")
	}
	for i := 0; i < 70; i++ {
		e.recordManagement("/api/nodes/private-id", "GET", 1, time.Now(), "ok", 200)
	}
	if len(e.managementEvents) != 64 {
		t.Fatal("unbounded logs")
	}
}
