// Package control manages accounts and H2 nodes. It never handles tunnel payloads.
package control

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"tunnelx/internal/config"
)

const Version = "v0.4.0"
const ConsoleVersion = "v0.6.17"

type Config struct {
	Listen        string           `json:"listen"`
	PublicURL     string           `json:"public_url"`
	DataFile      string           `json:"data_file"`
	AdminEmail    string           `json:"admin_email"`
	AdminPassword string           `json:"admin_password"`
	Registration  bool             `json:"registration"`
	TrialHours    int              `json:"trial_hours"`
	DatabaseURL   string           `json:"database_url,omitempty"`
	Commercial    CommercialConfig `json:"commercial,omitempty"`
	Mail          MailConfig       `json:"mail,omitempty"`
}

func (c *Config) Validate() error {
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("control listener must be a literal loopback address behind HTTPS")
	}
	u, e := url.Parse(c.PublicURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid public_url")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()) {
		return errors.New("public_url requires HTTPS; loopback HTTP is allowed for tests")
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if e := validateCommercialConfig(c); e != nil {
		return e
	}
	if c.DataFile == "" || !validEmail(c.AdminEmail) || len(c.AdminPassword) < 16 || len(c.AdminPassword) > 256 || c.TrialHours < 0 || c.TrialHours > 168 {
		return errors.New("invalid store, bootstrap admin credentials or trial duration")
	}
	return nil
}

type User struct {
	QuotaExhausted         bool   `json:"-"` // Derived access state, separate from subscription expiry.
	ID                     string `json:"id"`
	Email                  string `json:"email"`
	PasswordHash           string `json:"password_hash"`
	TunnelToken            string `json:"tunnel_token"`
	Role                   string `json:"role"`
	Disabled               bool   `json:"disabled"`
	ExpiresAt              int64  `json:"expires_at"`
	TrialUsed              bool   `json:"trial_used"`
	Upload                 int64  `json:"upload"`
	Download               int64  `json:"download"`
	Limit                  int64  `json:"limit"`
	Devices                int    `json:"devices"`
	CreatedAt              int64  `json:"created_at"`
	EmailVerifiedAt        int64  `json:"email_verified_at,omitempty"`
	NeedsEmailVerification bool   `json:"needs_email_verification,omitempty"`
	Beta                   bool   `json:"beta,omitempty"`
	MFASecret              string `json:"mfa_secret,omitempty"`
	MFALastStep            int64  `json:"mfa_last_step,omitempty"`
	RecoveryHashes         string `json:"recovery_hashes,omitempty"`
}

func (u User) Active(now int64) bool {
	return !u.Disabled && !u.QuotaExhausted && u.ExpiresAt > now && (u.Limit == 0 || u.Upload+u.Download < u.Limit)
}
func (u User) Public() map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "role": u.Role, "disabled": u.Disabled, "active": u.Active(time.Now().Unix()), "expires_at": u.ExpiresAt, "trial_used": u.TrialUsed, "upload": u.Upload, "download": u.Download, "traffic_limit": u.Limit, "device_limit": u.Devices, "created_at": u.CreatedAt, "email_verified": u.EmailVerifiedAt > 0, "verification_required": u.NeedsEmailVerification && u.EmailVerifiedAt == 0, "mfa_enabled": u.MFASecret != "", "beta": u.Beta}
}

type Node struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Region         string        `json:"region"`
	Enabled        bool          `json:"enabled"`
	Client         config.Client `json:"client"`
	CAPEM          string        `json:"ca_pem"`
	AgentKey       string        `json:"agent_key"`
	LastSeen       int64         `json:"last_seen"`
	Active         int64         `json:"active"`
	Version        string        `json:"version"`
	Setup          *NodeSetup    `json:"setup,omitempty"`
	TestOnly       bool          `json:"test_only,omitempty"`
	Capabilities   []string      `json:"capabilities,omitempty"`
	LastProbe      int64         `json:"last_probe,omitempty"`
	ProbeOK        bool          `json:"probe_ok"`
	ProbeLatencyMS int64         `json:"probe_latency_ms,omitempty"`
}

type NodeSetup struct {
	Domain          string `json:"domain"`
	CertificateMode string `json:"certificate_mode,omitempty"`
	TokenHash       string `json:"token_hash"`
	ExpiresAt       int64  `json:"expires_at"`
	CompletedAt     int64  `json:"completed_at"`
	Error           string `json:"error,omitempty"`
}

func (n Node) Pending() bool { return n.Setup != nil && n.Setup.CompletedAt == 0 }

func (n Node) Public() map[string]any {
	v := map[string]any{"id": n.ID, "name": n.Name, "region": n.Region, "enabled": n.Enabled, "transport": "h2", "privacy": n.Client.Privacy, "server_name": n.Client.ServerName, "server_ip": n.Client.ServerIP, "port": n.Client.Port, "online": n.LastSeen > time.Now().Unix()-90, "last_seen": n.LastSeen, "active": n.Active, "version": n.Version, "pending_install": n.Pending()}
	v["test_only"] = n.TestOnly
	v["capabilities"] = n.Capabilities
	v["last_probe"] = n.LastProbe
	v["probe_ok"] = n.ProbeOK
	v["probe_latency_ms"] = n.ProbeLatencyMS
	if n.Setup != nil {
		v["public_domain"] = n.Setup.Domain
		mode := n.Setup.CertificateMode
		if mode == "" {
			mode = "public"
		}
		v["certificate_mode"] = mode
		v["install_expires_at"] = n.Setup.ExpiresAt
		v["install_error"] = n.Setup.Error
	}
	return v
}

type Plan struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Days         int      `json:"days"`
	PriceCents   int64    `json:"price_cents"`
	TrafficBytes int64    `json:"traffic_bytes"`
	Devices      int      `json:"devices"`
	Enabled      bool     `json:"enabled"`
	Kind         string   `json:"kind,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	NodeIDs      []string `json:"node_ids,omitempty"`
}
type Order struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Plan      Plan   `json:"plan"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	PaidAt    int64  `json:"paid_at"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	PaymentID string `json:"payment_id,omitempty"`
	Test      bool   `json:"test,omitempty"`
}
type Session struct {
	Hash      string `json:"hash"`
	UserID    string `json:"user_id"`
	CSRF      string `json:"csrf"`
	ExpiresAt int64  `json:"expires_at"`
	MFA       bool   `json:"mfa,omitempty"`
}
type Audit struct {
	Time        int64         `json:"time"`
	Actor       string        `json:"actor"`
	Action      string        `json:"action"`
	Subject     string        `json:"subject"`
	ActorName   string        `json:"actor_name,omitempty"`
	SubjectName string        `json:"subject_name,omitempty"`
	Summary     string        `json:"summary,omitempty"`
	Changes     []AuditChange `json:"changes,omitempty"`
}
type Counter struct {
	UserID   string `json:"user_id"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}
type SyncRequest struct {
	RecoveredLeases []string  `json:"recovered_leases,omitempty"`
	BootID          string    `json:"boot_id"`
	Version         string    `json:"version"`
	Active          int64     `json:"active"`
	Counters        []Counter `json:"counters"`
	Capabilities    []string  `json:"capabilities,omitempty"`
}
type Grant struct {
	UserID        string `json:"user_id"`
	Token         string `json:"token"`
	ExpiresAt     int64  `json:"expires_at"`
	Remaining     int64  `json:"remaining"`
	Unlimited     bool   `json:"unlimited"`
	Devices       int    `json:"devices"`
	RequireLeases bool   `json:"require_leases,omitempty"`
}
type SyncResponse struct {
	ClearedRecovery []string  `json:"cleared_recovery,omitempty"`
	ValidForSeconds int       `json:"valid_for_seconds"`
	Grants          []Grant   `json:"grants"`
	Acknowledged    []Counter `json:"acknowledged"`
}
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes"`
}
type State struct {
	Schema        int                `json:"schema"`
	Users         []User             `json:"users"`
	Nodes         []Node             `json:"nodes"`
	Plans         []Plan             `json:"plans"`
	Orders        []Order            `json:"orders"`
	Sessions      []Session          `json:"sessions"`
	Audit         []Audit            `json:"audit"`
	Reports       map[string]Counter `json:"reports"`
	Announcement  string             `json:"announcement"`
	Release       Release            `json:"release"`
	Access        *AccessSettings    `json:"access,omitempty"`
	Payments      []Payment          `json:"payments,omitempty"`
	PaymentEvents []PaymentEvent     `json:"payment_events,omitempty"`
	PaymentConfig *GatewaySettings   `json:"payment_config,omitempty"`
	Refunds       []Refund           `json:"refunds,omitempty"`
	Entitlements  []Entitlement      `json:"entitlements,omitempty"`
	Challenges    []Challenge        `json:"challenges,omitempty"`
	Outbox        []MailMessage      `json:"outbox,omitempty"`
	Tickets       []Ticket           `json:"tickets,omitempty"`
	Incidents     []Incident         `json:"incidents,omitempty"`
	Invites       []BetaInvite       `json:"invites,omitempty"`
	Leases        []Lease            `json:"leases,omitempty"`
	TestUsage     map[string]Counter `json:"test_usage,omitempty"`
}

type AccessSettings struct {
	Registration bool `json:"registration"`
	TrialHours   int  `json:"trial_hours"`
}

func (a *API) accessSettings(d *State) AccessSettings {
	if a.Config.Commercial.Enabled && a.Config.Mail.Mode != "smtp" && a.Config.Mail.Mode != "test" {
		return AccessSettings{}
	}
	if d.Access != nil {
		value := *d.Access
		if a.Config.Commercial.Enabled {
			value.TrialHours = 0
		}
		return value
	}
	value := AccessSettings{Registration: a.Config.Registration, TrialHours: a.Config.TrialHours}
	if a.Config.Commercial.Enabled {
		value.TrialHours = 0
	}
	return value
}

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func validEmail(v string) bool {
	a, e := mail.ParseAddress(v)
	return e == nil && a.Address == v && len(v) <= 254 && strings.Contains(v, "@")
}
func passwordHash(v string) (string, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, v, salt, 600000, 32)
	if e != nil {
		return "", e
	}
	return "pbkdf2-sha256:600000:" + hex.EncodeToString(salt) + ":" + hex.EncodeToString(key), nil
}
func passwordValid(encoded, v string) bool {
	p := strings.Split(encoded, ":")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" || p[1] != "600000" {
		return false
	}
	salt, e := hex.DecodeString(p[2])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := hex.DecodeString(p[3])
	if e != nil || len(expected) != 32 {
		return false
	}
	key, e := pbkdf2.Key(sha256.New, v, salt, 600000, 32)
	return e == nil && subtle.ConstantTimeCompare(key, expected) == 1
}
func validatePlan(p Plan) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 || p.Days < 1 || p.Days > 3650 || p.PriceCents < 0 || p.PriceCents > 1e9 || p.TrafficBytes < 0 || p.TrafficBytes > 1<<50 || p.Devices < 1 || p.Devices > 32 {
		return errors.New("invalid plan")
	}
	return nil
}
func extend(u *User, days int, quota int64, devices int) error {
	if days < 1 || days > 3650 || quota < 0 || quota > 1<<50 || devices < 1 || devices > 32 {
		return errors.New("invalid entitlement")
	}
	now := time.Now().Unix()
	wasUnlimited := u.Limit == 0 && u.ExpiresAt > now
	if u.ExpiresAt < now {
		u.ExpiresAt = now
	}
	u.ExpiresAt += int64(days) * 86400
	u.Devices = devices
	if quota == 0 || wasUnlimited {
		u.Limit = 0
	} else {
		base := u.Limit
		if base < u.Upload+u.Download {
			base = u.Upload + u.Download
		}
		u.Limit = base + quota
	}
	return nil
}
func findUser(d *State, id string) *User {
	for i := range d.Users {
		if d.Users[i].ID == id {
			return &d.Users[i]
		}
	}
	return nil
}
func findNode(d *State, id string) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}
	return nil
}
func record(d *State, actor, action, subject string) {
	// The historical action encoded free-form reconciliation notes. New audit
	// entries keep a stable action code and never copy that text into the log.
	if strings.HasPrefix(action, "lease_reconciled:") {
		action = "lease_reconciled"
	}
	d.Audit = append(d.Audit, Audit{Time: time.Now().Unix(), Actor: actor, Action: action, Subject: subject})
}

// RequestError exposes the status without including untrusted response bodies.
type RequestError struct{ Status int }

func (e *RequestError) Error() string {
	return fmt.Sprintf("control request failed: HTTP %d", e.Status)
}
func safeError(status int) error { return &RequestError{Status: status} }
