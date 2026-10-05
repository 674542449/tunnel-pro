package control

type CommercialConfig struct {
	Enabled         bool   `json:"enabled"`
	PaymentMode     string `json:"payment_mode,omitempty"` // disabled, test, stripe, gateways
	PaymentSecret   string `json:"payment_secret,omitempty"`
	WebhookSecret   string `json:"webhook_secret,omitempty"`
	SecurityKey     string `json:"security_key,omitempty"`
	RequireAdminMFA bool   `json:"require_admin_mfa,omitempty"`
	BetaInviteOnly  bool   `json:"beta_invite_only,omitempty"`
	Terms           string `json:"terms,omitempty"`
	Privacy         string `json:"privacy,omitempty"`
	RefundPolicy    string `json:"refund_policy,omitempty"`
	LiveApproved    bool   `json:"live_approved,omitempty"`
}
type MailConfig struct {
	AlertsTo string `json:"alerts_to,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from,omitempty"`
}
type Payment struct {
	ID            string          `json:"id"`
	OrderID       string          `json:"order_id"`
	Provider      string          `json:"provider"`
	Reference     string          `json:"reference"`
	AmountCents   int64           `json:"amount_cents"`
	Currency      string          `json:"currency"`
	Status        string          `json:"status"`
	CreatedAt     int64           `json:"created_at"`
	PaidAt        int64           `json:"paid_at,omitempty"`
	Test          bool            `json:"test"`
	CheckoutURL   string          `json:"checkout_url,omitempty"`
	IntentID      string          `json:"intent_id,omitempty"`
	Method        string          `json:"method,omitempty"`
	TradeType     string          `json:"trade_type,omitempty"`
	Gateway       *PaymentGateway `json:"gateway,omitempty"`
	CheckoutState string          `json:"checkout_state,omitempty"`
}

// Gateway secrets are sealed with the server security key before persistence.
type GatewaySettings struct {
	Mode    string        `json:"mode"`
	Epay    GatewayConfig `json:"epay"`
	Bepusdt GatewayConfig `json:"bepusdt"`
}
type GatewayConfig struct {
	Enabled    bool     `json:"enabled"`
	URL        string   `json:"url"`
	MerchantID string   `json:"merchant_id,omitempty"`
	Secret     string   `json:"secret,omitempty"`
	Alipay     bool     `json:"alipay,omitempty"`
	Wechat     bool     `json:"wechat,omitempty"`
	TradeType  string   `json:"trade_type,omitempty"`
	TradeTypes []string `json:"trade_types,omitempty"`
}

// A payment retains its original merchant and key across settings changes.
type PaymentGateway struct {
	URL        string `json:"url"`
	MerchantID string `json:"merchant_id,omitempty"`
	Secret     string `json:"secret"`
	TradeType  string `json:"trade_type,omitempty"`
}

func (p Payment) Public() Payment {
	if p.Gateway != nil {
		p.TradeType = p.Gateway.TradeType
	}
	p.Gateway = nil
	return p
}

type PaymentEvent struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id"`
	ObjectID   string `json:"object_id"`
	Type       string `json:"type"`
	BodyHash   string `json:"body_hash"`
	Time       int64  `json:"time"`
}
type Refund struct {
	ID          string `json:"id"`
	PaymentID   string `json:"payment_id"`
	Reference   string `json:"reference"`
	AmountCents int64  `json:"amount_cents"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	CreatedAt   int64  `json:"created_at"`
	Test        bool   `json:"test"`
}

// Entitlements are immutable grants, individually revocable. Usage is cumulative.
type Entitlement struct {
	PlanID     string   `json:"plan_id,omitempty"`
	PlanName   string   `json:"plan_name,omitempty"`
	AssignedBy string   `json:"assigned_by,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	RequestID  string   `json:"request_id,omitempty"`
	AssignMode string   `json:"assign_mode,omitempty"`
	ID         string   `json:"id"`
	UserID     string   `json:"user_id"`
	OrderID    string   `json:"order_id"`
	Source     string   `json:"source"`
	StartsAt   int64    `json:"starts_at"`
	EndsAt     int64    `json:"ends_at"`
	Bytes      int64    `json:"bytes"`
	Devices    int      `json:"devices"`
	NodeIDs    []string `json:"node_ids,omitempty"`
	RevokedAt  int64    `json:"revoked_at,omitempty"`
	Test       bool     `json:"test"`
	Used       int64    `json:"used"`
	Kind       string   `json:"kind,omitempty"`
}
type Challenge struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Purpose   string `json:"purpose"`
	TokenHash string `json:"token_hash"`
	Secret    string `json:"secret,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at,omitempty"`
}
type MailMessage struct {
	ID          string `json:"id"`
	To          string `json:"to"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	CreatedAt   int64  `json:"created_at"`
	SentAt      int64  `json:"sent_at,omitempty"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt,omitempty"`
}
type TicketReply struct {
	ID      string `json:"id"`
	ActorID string `json:"actor_id"`
	Body    string `json:"body"`
	Time    int64  `json:"time"`
}
type Ticket struct {
	ID        string        `json:"id"`
	UserID    string        `json:"user_id"`
	Subject   string        `json:"subject"`
	Status    string        `json:"status"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64         `json:"updated_at"`
	Replies   []TicketReply `json:"replies"`
}
type Incident struct {
	NotifiedAt         int64  `json:"notified_at,omitempty"`
	RecoveryNotifiedAt int64  `json:"recovery_notified_at,omitempty"`
	ID                 string `json:"id"`
	Key                string `json:"key"`
	NodeID             string `json:"node_id,omitempty"`
	Severity           string `json:"severity"`
	Message            string `json:"message"`
	OpenedAt           int64  `json:"opened_at"`
	ResolvedAt         int64  `json:"resolved_at,omitempty"`
	AcknowledgedAt     int64  `json:"acknowledged_at,omitempty"`
}
type BetaInvite struct {
	ID        string `json:"id"`
	TokenHash string `json:"token_hash"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}
type Lease struct {
	Uncertain   bool              `json:"uncertain,omitempty"`
	ID          string            `json:"id"`
	NodeID      string            `json:"node_id"`
	UserID      string            `json:"user_id"`
	DeviceID    string            `json:"device_id"`
	ExpiresAt   int64             `json:"expires_at"`
	Budget      int64             `json:"budget"`
	Used        int64             `json:"used"`
	Test        bool              `json:"test"`
	Closed      bool              `json:"closed"`
	Allocations []LeaseAllocation `json:"allocations"`
}
type LeaseAllocation struct {
	EntitlementID string `json:"entitlement_id"`
	Budget        int64  `json:"budget"`
	Used          int64  `json:"used"`
}
type LeaseRequest struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	Used     int64  `json:"used"`
	Close    bool   `json:"close"`
}
