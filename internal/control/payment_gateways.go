package control

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *API) paymentMode(d *State) string {
	if d.PaymentConfig != nil && d.PaymentConfig.Mode != "" {
		return d.PaymentConfig.Mode
	}
	return a.Config.Commercial.PaymentMode
}

type paymentMethod struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Network   string `json:"network,omitempty"`
	TradeType string `json:"trade_type,omitempty"`
}

var usdtNetworks = []paymentMethod{
	{ID: "usdt_trc20", Name: "USDT · TRC20", Network: "TRON", TradeType: "usdt.trc20"},
	{ID: "usdt_bep20", Name: "USDT · BEP20", Network: "BNB Smart Chain", TradeType: "usdt.bep20"},
	{ID: "usdt_polygon", Name: "USDT · Polygon", Network: "Polygon (POL)", TradeType: "usdt.polygon"},
}

// Old settings had a single default chain. Unset lists now offer all three;
// pending payments still retain the exact network in their private snapshot.
func usdtTradeTypes(g GatewayConfig) []string {
	if g.TradeTypes != nil {
		return append([]string{}, g.TradeTypes...)
	}
	return []string{"usdt.trc20", "usdt.bep20", "usdt.polygon"}
}

func includesTradeType(types []string, value string) bool {
	for _, candidate := range types {
		if candidate == value {
			return true
		}
	}
	return false
}

func (a *API) paymentMethods(d *State) []paymentMethod {
	result := []paymentMethod{}
	switch a.paymentMode(d) {
	case "test":
		return []paymentMethod{{ID: "test", Name: "模拟付款（不扣款）"}}
	case "stripe":
		return []paymentMethod{{ID: "stripe", Name: "银行卡 / Stripe"}}
	case "gateways":
		if d.PaymentConfig == nil {
			return result
		}
		e, b := d.PaymentConfig.Epay, d.PaymentConfig.Bepusdt
		if e.Enabled && e.Secret != "" && e.URL != "" && e.MerchantID != "" {
			if e.Alipay {
				result = append(result, paymentMethod{ID: "alipay", Name: "支付宝"})
			}
			if e.Wechat {
				result = append(result, paymentMethod{ID: "wxpay", Name: "微信支付"})
			}
		}
		if b.Enabled && b.Secret != "" && b.URL != "" {
			for _, network := range usdtNetworks {
				if includesTradeType(usdtTradeTypes(b), network.TradeType) {
					result = append(result, network)
				}
			}
		}
	}
	return result
}

func (a *API) gatewayURL(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(raw) > 2048 {
		return "", errors.New("网关地址需要完整的 HTTPS 地址，不含密钥、查询参数或片段")
	}
	local := false
	if origin, e := url.Parse(a.Config.PublicURL); e == nil {
		ip := net.ParseIP(origin.Hostname())
		local = origin.Scheme == "http" && ip != nil && ip.IsLoopback()
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(local && u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return "", errors.New("网关必须使用 HTTPS")
	}
	if !local && (strings.EqualFold(u.Hostname(), "localhost") || ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast())) {
		return "", errors.New("生产支付网关必须是公开 HTTPS 地址")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(u.Path, ".php") {
		return "", errors.New("请填写网关基础地址，不要包含 submit.php 或 mapi.php")
	}
	return u.String(), nil
}

func (a *API) gatewaySettingsPublic(d *State) map[string]any {
	c := GatewaySettings{Mode: a.paymentMode(d)}
	if d.PaymentConfig != nil {
		c = *d.PaymentConfig
	}
	public := func(g GatewayConfig) map[string]any {
		return map[string]any{"enabled": g.Enabled, "url": g.URL, "merchant_id": g.MerchantID, "key_configured": g.Secret != "", "alipay": g.Alipay, "wechat": g.Wechat, "trade_type": g.TradeType}
	}
	bepusdt := public(c.Bepusdt)
	bepusdt["trade_types"] = usdtTradeTypes(c.Bepusdt)
	return map[string]any{"mode": c.Mode, "epay": public(c.Epay), "bepusdt": bepusdt, "usdt_networks": usdtNetworks, "methods": a.paymentMethods(d), "callbacks": map[string]string{"epay": a.Config.PublicURL + "/api/payment/webhook/epay", "bepusdt": a.Config.PublicURL + "/api/payment/webhook/bepusdt"}, "live_ready": a.readiness()["live_ready"], "live_approved": a.Config.Commercial.LiveApproved}
}

type gatewayInput struct {
	Enabled    bool     `json:"enabled"`
	URL        string   `json:"url"`
	MerchantID string   `json:"merchant_id"`
	Key        string   `json:"key"`
	ClearKey   bool     `json:"clear_key"`
	Alipay     bool     `json:"alipay"`
	Wechat     bool     `json:"wechat"`
	TradeType  string   `json:"trade_type"`
	TradeTypes []string `json:"trade_types"`
}

func (a *API) saveGateway(old GatewayConfig, in gatewayInput, provider string) (GatewayConfig, error) {
	g := GatewayConfig{Enabled: in.Enabled, URL: strings.TrimSpace(in.URL), MerchantID: strings.TrimSpace(in.MerchantID), Secret: old.Secret, Alipay: in.Alipay, Wechat: in.Wechat, TradeType: strings.TrimSpace(in.TradeType)}
	if g.URL != "" {
		var e error
		g.URL, e = a.gatewayURL(g.URL)
		if e != nil {
			return g, e
		}
	}
	if len(g.MerchantID) > 64 || strings.ContainsAny(g.MerchantID, "&=\r\n\t ") {
		return g, errors.New("商户号无效")
	}
	if in.ClearKey {
		g.Secret = ""
	}
	if in.Key != "" {
		if len(in.Key) < 8 || len(in.Key) > 1024 || strings.ContainsAny(in.Key, "\r\n") {
			return g, errors.New("密钥长度需要 8 至 1024 字节，不能含换行")
		}
		var e error
		g.Secret, e = a.seal("payment-gateway", in.Key)
		if e != nil {
			return g, e
		}
	}
	if provider == "bepusdt" {
		g.MerchantID = ""
		g.Alipay = false
		g.Wechat = false
		types := in.TradeTypes
		if types == nil {
			types = usdtTradeTypes(old)
		}
		if len(types) == 0 || len(types) > len(usdtNetworks) {
			return g, errors.New("请至少选择一个 USDT 网络")
		}
		for _, value := range types {
			valid := false
			for _, network := range usdtNetworks {
				valid = valid || value == network.TradeType
			}
			if !valid {
				return g, errors.New("USDT 仅支持 TRC20、BEP20 和 Polygon")
			}
		}
		for _, network := range usdtNetworks {
			if includesTradeType(types, network.TradeType) {
				g.TradeTypes = append(g.TradeTypes, network.TradeType)
			}
		}
		if g.TradeType != "" && !includesTradeType(g.TradeTypes, g.TradeType) {
			return g, errors.New("默认 USDT 网络必须在已选择的网络中")
		}
		if g.TradeType == "" {
			g.TradeType = g.TradeTypes[0]
		}
	}
	if g.Enabled && (g.URL == "" || g.Secret == "" || provider == "epay" && (g.MerchantID == "" || !g.Alipay && !g.Wechat)) {
		return g, errors.New("启用渠道前需要填写网关、密钥及商户号，并选择支付方式")
	}
	return g, nil
}

func (a *API) adminGatewaySettings(w http.ResponseWriter, r *http.Request, u *User, s *Session, path string, parts []string) bool {
	if path == "payment-settings" {
		if u.Role != "admin" {
			fail(w, 403, "只有管理员可以配置支付渠道")
			return true
		}
		if r.Method == "GET" {
			d := a.Store.Snapshot()
			reply(w, 200, a.gatewaySettingsPublic(&d))
			return true
		}
		if r.Method != "POST" {
			fail(w, 405, "请求方法无效")
			return true
		}
		var b struct {
			Mode    string       `json:"mode"`
			Epay    gatewayInput `json:"epay"`
			Bepusdt gatewayInput `json:"bepusdt"`
		}
		if !decode(w, r, &b) {
			return true
		}
		if b.Mode != "test" && b.Mode != "disabled" && b.Mode != "gateways" && b.Mode != "stripe" {
			fail(w, 400, "支付模式无效")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			old := GatewaySettings{}
			if d.PaymentConfig != nil {
				old = *d.PaymentConfig
			}
			ep, e := a.saveGateway(old.Epay, b.Epay, "epay")
			if e != nil {
				return e
			}
			be, e := a.saveGateway(old.Bepusdt, b.Bepusdt, "bepusdt")
			if e != nil {
				return e
			}
			candidate := GatewaySettings{Mode: b.Mode, Epay: ep, Bepusdt: be}
			d.PaymentConfig = &candidate
			if b.Mode == "gateways" && len(a.paymentMethods(d)) == 0 {
				return errors.New("正式渠道模式至少需要一个配置完整的支付方式")
			}
			record(d, u.ID, "payment_settings_saved", "")
			return nil
		})
		if e != nil {
			failCommit(w, e, 400, "支付配置未保存")
			return true
		}
		d := a.Store.Snapshot()
		reply(w, 200, a.gatewaySettingsPublic(&d))
		return true
	}
	if len(parts) == 3 && parts[0] == "payments" && parts[2] == "manual-refund" && r.Method == "POST" {
		var b struct {
			Reason    string `json:"reason"`
			Reference string `json:"reference"`
		}
		if !decode(w, r, &b) {
			return true
		}
		b.Reason = strings.TrimSpace(b.Reason)
		b.Reference = strings.TrimSpace(b.Reference)
		if len(b.Reason) < 4 || len(b.Reason) > 1000 || len(b.Reference) < 6 || len(b.Reference) > 256 {
			fail(w, 400, "请填写退款原因和已完成退款的渠道流水号或交易哈希")
			return true
		}
		e := a.commit(u, s, func(d *State) error {
			p := findPayment(d, parts[1])
			if p == nil || p.Test || (p.Provider != "epay" && p.Provider != "bepusdt") {
				return errors.New("此付款不支持外部退款确认")
			}
			if p.Status == "refunded" {
				return nil
			}
			if e := refundPayment(d, p.ID, "manual-refund:"+b.Reference, b.Reason, time.Now().Unix(), u.ID); e != nil {
				return e
			}
			record(d, u.ID, "external_refund_confirmed", p.ID)
			return nil
		})
		if e != nil {
			failCommit(w, e, 409, "退款确认未保存，请核实付款状态")
			return true
		}
		reply(w, 200, map[string]bool{"refunded": true, "external_refund_recorded": true})
		return true
	}
	return false
}

// Both protocols concatenate raw sorted values and append the shared key.
// This MD5 is gateway authentication for compatibility, not tunnel encryption.
func gatewaySign(values map[string]string, key string, excluded ...string) string {
	keys := []string{}
	for k, v := range values {
		skip := v == ""
		for _, x := range excluded {
			if k == x {
				skip = true
			}
		}
		if !skip {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	h := md5.Sum([]byte(strings.Join(parts, "&") + key))
	return hex.EncodeToString(h[:])
}
func signatureMatches(got, want string) bool {
	return len(got) == 32 && subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(want)) == 1
}
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

var moneyPattern = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{1,2})?$`)

func moneyCents(raw string) (int64, error) {
	if !moneyPattern.MatchString(raw) {
		return 0, errors.New("invalid money")
	}
	whole, fraction, _ := strings.Cut(raw, ".")
	v, e := strconv.ParseInt(whole, 10, 64)
	if e != nil {
		return 0, e
	}
	if len(fraction) == 1 {
		fraction += "0"
	}
	part := int64(0)
	if fraction != "" {
		part, e = strconv.ParseInt(fraction, 10, 64)
	}
	if e != nil || v > 1e7 {
		return 0, errors.New("invalid money")
	}
	return v*100 + part, nil
}
func callbackText(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	io.WriteString(w, value)
}

func gatewayForMethod(d *State, method string) (string, *PaymentGateway, error) {
	if d.PaymentConfig == nil {
		return "", nil, errors.New("payment gateway unconfigured")
	}
	g := d.PaymentConfig.Epay
	provider := "epay"
	tradeType := ""
	for _, network := range usdtNetworks {
		if method == network.ID {
			tradeType = network.TradeType
		}
	}
	if method == "usdt" || tradeType != "" {
		g = d.PaymentConfig.Bepusdt
		provider = "bepusdt"
		if method == "usdt" {
			tradeType = g.TradeType
			if tradeType == "" && len(usdtTradeTypes(g)) > 0 {
				tradeType = usdtTradeTypes(g)[0]
			}
		}
		if !includesTradeType(usdtTradeTypes(g), tradeType) {
			return "", nil, errors.New("USDT 网络未启用")
		}
		g.TradeType = tradeType
	} else if method != "alipay" && method != "wxpay" {
		return "", nil, errors.New("invalid payment method")
	}
	if !g.Enabled || g.Secret == "" || method == "alipay" && !g.Alipay || method == "wxpay" && !g.Wechat {
		return "", nil, errors.New("payment method disabled")
	}
	return provider, &PaymentGateway{URL: g.URL, MerchantID: g.MerchantID, Secret: g.Secret, TradeType: g.TradeType}, nil
}

func (a *API) checkoutGateway(ctx context.Context, p Payment, o Order) (string, string, error) {
	if p.Gateway == nil || p.Test || p.AmountCents <= 0 {
		return "", "", errors.New("invalid gateway payment")
	}
	key, e := a.unseal("payment-gateway", p.Gateway.Secret)
	if e != nil {
		return "", "", e
	}
	if p.Provider == "epay" {
		if p.Currency != "cny" {
			return "", "", errors.New("易支付只支持人民币套餐")
		}
		fields := map[string]string{"pid": p.Gateway.MerchantID, "type": p.Method, "out_trade_no": p.ID, "notify_url": a.Config.PublicURL + "/api/payment/webhook/epay", "return_url": a.Config.PublicURL + "/#orders", "name": o.Plan.Name, "money": money(p.AmountCents)}
		fields["sign"] = gatewaySign(fields, key, "sign", "sign_type")
		fields["sign_type"] = "MD5"
		v := url.Values{}
		for k, value := range fields {
			v.Set(k, value)
		}
		return "", p.Gateway.URL + "/submit.php?" + v.Encode(), nil
	}
	if p.Provider != "bepusdt" {
		return "", "", errors.New("invalid gateway provider")
	}
	// The exact fixed fiat amount is positive; address-exclusive donations are
	// never used for a fixed-price purchase. The gateway owns the wallet address.
	amount := json.Number(money(p.AmountCents))
	fields := map[string]any{"order_id": p.ID, "amount": amount, "fiat": strings.ToUpper(p.Currency), "trade_type": p.Gateway.TradeType, "name": o.Plan.Name, "notify_url": a.Config.PublicURL + "/api/payment/webhook/bepusdt", "redirect_url": a.Config.PublicURL + "/#orders", "timeout": 1800}
	signed, e := bepusdtFields(fields)
	if e != nil {
		return "", "", e
	}
	fields["signature"] = gatewaySign(signed, key, "signature")
	raw, _ := json.Marshal(fields)
	req, e := http.NewRequestWithContext(ctx, "POST", p.Gateway.URL+"/api/v1/order/create-transaction", bytes.NewReader(raw))
	if e != nil {
		return "", "", e
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.PaymentHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, e := client.Do(req)
	if e != nil {
		return "", "", errors.New("USDT 网关暂时不可用")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", "", errors.New("USDT 网关返回错误状态")
	}
	var result struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			TradeID    string          `json:"trade_id"`
			OrderID    string          `json:"order_id"`
			Amount     json.RawMessage `json:"amount"`
			Fiat       string          `json:"fiat"`
			PaymentURL string          `json:"payment_url"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if decoder.Decode(&result) != nil || result.StatusCode != 200 || result.Data.OrderID != p.ID || result.Data.TradeID == "" || len(result.Data.TradeID) > 128 || !strings.EqualFold(result.Data.Fiat, p.Currency) {
		return "", "", errors.New("USDT 下单响应不匹配")
	}
	responseAmount := strings.Trim(string(result.Data.Amount), "\"")
	cents, e := moneyCents(responseAmount)
	if e != nil || cents != p.AmountCents {
		return "", "", errors.New("USDT 下单金额不匹配")
	}
	target, e := url.Parse(result.Data.PaymentURL)
	base, _ := url.Parse(p.Gateway.URL)
	if e != nil || target.User != nil || target.Host != base.Host || target.Scheme != base.Scheme || target.Fragment != "" {
		return "", "", errors.New("USDT 收银台地址不属于配置的网关")
	}
	return result.Data.TradeID, target.String(), nil
}

func (a *API) createGatewayCheckout(w http.ResponseWriter, r *http.Request, u *User, s *Session, p Payment, o Order) {
	// BEpusdt recreates an existing order_id, so retrying after an ambiguous
	// network failure can change a payment already displayed to the customer.
	if p.Provider == "bepusdt" {
		err := a.commit(u, s, func(d *State) error {
			current := findPayment(d, p.ID)
			if current == nil || current.Status != "pending" || current.CheckoutState != "" {
				return errors.New("USDT 收银台已创建或结果待核实，请查看付款记录")
			}
			current.CheckoutState = "creating"
			return nil
		})
		if err != nil {
			failCommit(w, err, 409, "USDT 收银台正在创建或结果待核实，请不要重复下单")
			return
		}
	}
	ref, target, err := a.checkoutGateway(r.Context(), p, o)
	if err != nil {
		if p.Provider == "bepusdt" {
			_ = a.Store.Update(func(d *State) error {
				if current := findPayment(d, p.ID); current != nil && current.Status == "pending" && current.CheckoutURL == "" {
					current.CheckoutState = "unknown"
					incident(d, "checkout-unknown:"+p.ID, "", "USDT 网关下单结果待核实，请在网关检查同一商户订单号，避免重复创建付款。", true, time.Now().Unix())
				}
				return nil
			})
		}
		fail(w, 502, "支付网关未完成下单或结果待核实，请查看付款记录并联系管理员")
		return
	}
	err = a.Store.Update(func(d *State) error {
		current := findPayment(d, p.ID)
		if current == nil {
			return errors.New("missing payment")
		}
		if ref != "" && current.Reference != "" && current.Reference != ref {
			return errors.New("payment reference changed")
		}
		if ref != "" {
			current.Reference = ref
		}
		current.CheckoutURL = target
		current.CheckoutState = "ready"
		if current.Status != "pending" {
			current.CheckoutState = "confirmed"
		}
		incident(d, "checkout-unknown:"+p.ID, "", "", false, time.Now().Unix())
		return nil
	})
	if err != nil {
		fail(w, 503, "无法保存收银台，请先核实付款记录，不要重复付款")
		return
	}
	reply(w, 200, map[string]any{"payment_id": p.ID, "provider": p.Provider, "checkout_url": target})
}

// UseNumber prevents rounding away fractional money; signature formatting
// follows BEpusdt's Go fmt %v scalar convention, including zero and booleans.
func bepusdtFields(values map[string]any) (map[string]string, error) {
	result := map[string]string{}
	for k, v := range values {
		switch x := v.(type) {
		case nil:
			continue
		case string:
			result[k] = x
		case json.Number:
			f, e := strconv.ParseFloat(string(x), 64)
			if e != nil {
				return nil, e
			}
			result[k] = fmt.Sprint(f)
		case float64:
			result[k] = fmt.Sprint(x)
		case int:
			result[k] = strconv.Itoa(x)
		case bool:
			result[k] = strconv.FormatBool(x)
		default:
			return nil, errors.New("unsupported callback value")
		}
	}
	return result, nil
}
func readBepusdt(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.UseNumber()
	token, e := decoder.Token()
	if e != nil || token != json.Delim('{') {
		return nil, errors.New("invalid callback")
	}
	result := map[string]any{}
	for decoder.More() {
		token, e = decoder.Token()
		if e != nil {
			return nil, e
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid callback key")
		}
		if _, exists := result[key]; exists {
			return nil, errors.New("duplicate callback key")
		}
		var value any
		if decoder.Decode(&value) != nil {
			return nil, errors.New("invalid callback value")
		}
		result[key] = value
	}
	if _, e = decoder.Token(); e != nil {
		return nil, e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing callback data")
	}
	return result, nil
}

func (a *API) epayWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		callbackText(w, 405, "fail")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if len(r.URL.RawQuery) > 64<<10 || r.ParseForm() != nil {
		callbackText(w, 400, "fail")
		return
	}
	fields := map[string]string{}
	for k, values := range r.Form {
		if len(values) != 1 {
			callbackText(w, 400, "fail")
			return
		}
		fields[k] = values[0]
	}
	d := a.Store.Snapshot()
	p := findPayment(&d, fields["out_trade_no"])
	if p == nil || p.Test || p.Provider != "epay" || p.Gateway == nil {
		callbackText(w, 400, "fail")
		return
	}
	key, e := a.unseal("payment-gateway", p.Gateway.Secret)
	if e != nil || !signatureMatches(fields["sign"], gatewaySign(fields, key, "sign", "sign_type")) || fields["sign_type"] != "MD5" || fields["pid"] != p.Gateway.MerchantID || fields["type"] != p.Method || fields["trade_status"] != "TRADE_SUCCESS" || fields["trade_no"] == "" || len(fields["trade_no"]) > 128 {
		callbackText(w, 400, "fail")
		return
	}
	cents, e := moneyCents(fields["money"])
	if e != nil || cents != p.AmountCents || p.Currency != "cny" {
		callbackText(w, 400, "fail")
		return
	}
	e = a.Store.Update(func(d *State) error {
		current := findPayment(d, p.ID)
		if current == nil || current.Reference != "" && current.Reference != fields["trade_no"] {
			return errors.New("reference mismatch")
		}
		current.Reference = fields["trade_no"]
		return paidEvent(d, p.ID, "epay-paid:"+fields["trade_no"], cents, "cny", false, time.Now().Unix())
	})
	if e != nil {
		callbackText(w, 409, "fail")
		return
	}
	callbackText(w, 200, "success")
}

func (a *API) bepusdtWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		callbackText(w, 405, "fail")
		return
	}
	values, e := readBepusdt(w, r)
	if e != nil {
		callbackText(w, 400, "fail")
		return
	}
	fields, e := bepusdtFields(values)
	if e != nil {
		callbackText(w, 400, "fail")
		return
	}
	d := a.Store.Snapshot()
	p := findPayment(&d, fields["order_id"])
	if p == nil || p.Test || p.Provider != "bepusdt" || p.Gateway == nil {
		callbackText(w, 400, "fail")
		return
	}
	key, e := a.unseal("payment-gateway", p.Gateway.Secret)
	if e != nil || !signatureMatches(fields["signature"], gatewaySign(fields, key, "signature")) || fields["trade_id"] == "" || len(fields["trade_id"]) > 128 || p.Reference != "" && p.Reference != fields["trade_id"] {
		callbackText(w, 400, "fail")
		return
	}
	amount := fields["amount"]
	if numeric, ok := values["amount"].(json.Number); ok {
		amount = string(numeric)
	}
	cents, e := moneyCents(amount)
	if e != nil || cents != p.AmountCents {
		callbackText(w, 400, "fail")
		return
	}
	if fiat := fields["fiat"]; fiat != "" && !strings.EqualFold(fiat, p.Currency) {
		callbackText(w, 400, "fail")
		return
	}
	if fields["status"] != "1" && fields["status"] != "2" && fields["status"] != "3" {
		callbackText(w, 400, "fail")
		return
	}
	if fields["status"] == "2" && (fields["block_transaction_id"] == "" || len(fields["block_transaction_id"]) > 256 || fields["token"] == "" || fields["actual_amount"] == "") {
		callbackText(w, 400, "fail")
		return
	}
	if fields["status"] == "2" {
		v, e := strconv.ParseFloat(fields["actual_amount"], 64)
		if e != nil || !(v > 0) {
			callbackText(w, 400, "fail")
			return
		}
	}
	e = a.Store.Update(func(d *State) error {
		current := findPayment(d, p.ID)
		if current == nil || current.Reference != "" && current.Reference != fields["trade_id"] {
			return errors.New("reference mismatch")
		}
		current.Reference = fields["trade_id"]
		if fields["status"] == "2" {
			current.CheckoutState = "confirmed"
			incident(d, "checkout-unknown:"+p.ID, "", "", false, time.Now().Unix())
			return paidEvent(d, p.ID, "bepusdt-paid:"+fields["trade_id"], cents, p.Currency, false, time.Now().Unix())
		}
		if fields["status"] == "3" && current.Status == "pending" {
			current.CheckoutState = "confirmed"
			incident(d, "checkout-unknown:"+p.ID, "", "", false, time.Now().Unix())
			current.Status = "expired"
			if o := findOrder(d, current.OrderID); o != nil && o.Status == "pending" {
				o.Status = "expired"
			}
			record(d, "system", "gateway_payment_expired", current.ID)
		}
		return nil
	})
	if e != nil {
		callbackText(w, 409, "fail")
		return
	}
	// 'ok' follows the callback retry documentation; the current gateway also
	// accepts every HTTP 200 response. Never acknowledge an uncommitted receipt.
	callbackText(w, 200, "ok")
}
