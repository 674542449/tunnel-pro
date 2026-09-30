package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"tunnel/internal/mux"
	"tunnel/internal/proto"
	"tunnel/internal/relay"
	"tunnel/internal/socks5"

	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
	wailsRT "github.com/wailsapp/wails/v2/pkg/runtime"
)

const apiBase = "https://cloud.xiaguamail.com"
const clientVersion = "1.0.0"

var uaPool = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
}

var flagMap = map[string]string{
	"US": "\U0001F1FA\U0001F1F8", "JP": "\U0001F1EF\U0001F1F5", "DE": "\U0001F1E9\U0001F1EA",
	"SG": "\U0001F1F8\U0001F1EC", "KR": "\U0001F1F0\U0001F1F7", "HK": "\U0001F1ED\U0001F1F0",
	"TW": "\U0001F1F9\U0001F1FC", "GB": "\U0001F1EC\U0001F1E7", "FR": "\U0001F1EB\U0001F1F7",
	"CA": "\U0001F1E8\U0001F1E6", "AU": "\U0001F1E6\U0001F1FA", "NL": "\U0001F1F3\U0001F1F1",
	"IN": "\U0001F1EE\U0001F1F3", "RU": "\U0001F1F7\U0001F1FA", "BR": "\U0001F1E7\U0001F1F7",
	"TR": "\U0001F1F9\U0001F1F7",
}

type NodeInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Region  string `json:"region"`
	Flag    string `json:"flag"`
	Latency int    `json:"latency"`
}

type SpeedInfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

type StatusInfo struct {
	Connected   bool   `json:"connected"`
	NodeName    string `json:"nodeName"`
	NodeID      int64  `json:"nodeId"`
	ConnectedAt int64  `json:"connectedAt"`
}

type ProfileInfo struct {
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	Role         string     `json:"role"`
	Active       bool       `json:"active"`
	ExpiresAt    int64      `json:"expires_at"`
	CreatedAt    int64      `json:"created_at"`
	TrialUsed    bool       `json:"trial_used"`
	Upload       int64      `json:"upload"`
	Download     int64      `json:"download"`
	TrafficLimit int64      `json:"traffic_limit"`
	PlanID       int64      `json:"plan_id"`
	Plans        []PlanInfo `json:"plans"`
}

type PlanInfo struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Days         int     `json:"days"`
	Price        float64 `json:"price"`
	TrafficLimit int64   `json:"traffic_limit"`
}

type OrderInfo struct {
	ID        int64   `json:"id"`
	Plan      string  `json:"plan"`
	Days      int     `json:"days"`
	Amount    float64 `json:"amount"`
	Status    string  `json:"status"`
	CreatedAt int64   `json:"created_at"`
	PaidAt    int64   `json:"paid_at"`
}

type AuthState struct {
	Token      string          `json:"token,omitempty"`
	User       json.RawMessage `json:"user,omitempty"`
	LastNodeID int64           `json:"last_node_id,omitempty"`
	SavedEmail string          `json:"saved_email,omitempty"`
	Favorites  []int64         `json:"favorites,omitempty"`
	DarkMode   *bool           `json:"dark_mode,omitempty"`
	ProxyMode  string          `json:"proxy_mode,omitempty"`
	KillSwitch bool            `json:"kill_switch,omitempty"`
}

type serverConfig struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	IP     string `json:"ip,omitempty"`
	PSK    string `json:"psk"`
	ID     int64  `json:"id"`
	Region string `json:"region,omitempty"`
}

const muxPoolSize = 4

type UpdateInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	URL       string `json:"url"`
}

type AnnouncementInfo struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Level     string `json:"level"`
	CreatedAt int64  `json:"created_at"`
}

type ConnLogEntry struct {
	Time   int64  `json:"time"`
	Action string `json:"action"`
	Node   string `json:"node"`
	Detail string `json:"detail,omitempty"`
}

type App struct {
	ctx            context.Context
	auth           AuthState
	authPath       string
	nodes          []serverConfig
	mixedAddr      string
	pacPath        string
	trayDisconnect interface{ Enable(); Disable() }

	mu          sync.RWMutex
	pool        *mux.MuxPool
	activeID    int64
	proxyOn     bool
	upBytes     atomic.Int64
	downBytes   atomic.Int64
	connectedAt int64

	tunCmd      *exec.Cmd
	tunRunning  bool
	origGateway string

	connLog        []ConnLogEntry
	connLogMu      sync.Mutex
	killSwitch     bool
	speedLimitMbps int
	deviceLimit    int
	sessionID      string
}

func NewApp() *App {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	return &App{
		authPath:  filepath.Join(dir, "auth.json"),
		mixedAddr: "127.0.0.1:7890",
		pacPath:   filepath.Join(dir, "proxy.pac"),
		activeID:  -1,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadAuth()
	a.killSwitch = a.auth.KillSwitch
	a.cleanStaleProxy()
	go a.startMixed()
	go a.healthCheck()
	go a.startTray()
	go a.checkUpdateOnStart()
}

func (a *App) cleanStaleProxy() {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil {
		return
	}
	if !strings.Contains(string(out), "0x1") {
		return
	}
	out, err = exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return
	}
	if strings.Contains(string(out), a.mixedAddr) {
		clearSystemProxy()
		log.Println("[startup] cleared stale proxy from previous session")
	}
}

func (a *App) GetLastNodeID() int64 {
	return a.auth.LastNodeID
}

func (a *App) shutdown(ctx context.Context) {
	a.Disconnect()
}

func (a *App) loadAuth() {
	data, err := os.ReadFile(a.authPath)
	if err == nil {
		json.Unmarshal(data, &a.auth)
	}
}

func (a *App) saveAuth() {
	data, _ := json.MarshalIndent(a.auth, "", "  ")
	os.WriteFile(a.authPath, data, 0644)
}

func parseJWTUID(token string) int64 {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var c struct {
		UID int64 `json:"uid"`
	}
	json.Unmarshal(data, &c)
	return c.UID
}

func getMachineID() string {
	out, err := exec.Command("wmic", "csproduct", "get", "UUID").Output()
	if err != nil {
		return fmt.Sprintf("win-%d", time.Now().UnixNano())
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "UUID" {
			return line
		}
	}
	return fmt.Sprintf("win-%d", time.Now().UnixNano())
}

// ---------- Wails bindings ----------

func (a *App) Login(email, pass string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	resp, err := http.Post(apiBase+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth.Token = result.Token
	a.auth.User = result.User
	a.auth.SavedEmail = email
	a.saveAuth()
	return nil
}

func (a *App) Register(email, pass, inviteCode string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": pass, "invite_code": inviteCode})
	resp, err := http.Post(apiBase+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth.Token = result.Token
	a.auth.User = result.User
	a.auth.SavedEmail = email
	a.saveAuth()
	return nil
}

func (a *App) GuestLogin() error {
	machineID := getMachineID()
	body, _ := json.Marshal(map[string]string{"machine_id": machineID})
	resp, err := http.Post(apiBase+"/api/guest", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Token string          `json:"token"`
		User  json.RawMessage `json:"user"`
		Error string          `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	a.auth = AuthState{Token: result.Token, User: result.User}
	a.saveAuth()
	return nil
}

func (a *App) ActivateTrial() error {
	req, _ := http.NewRequest("POST", apiBase+"/api/trial", nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error")
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) IsLoggedIn() bool {
	return a.auth.Token != ""
}

func (a *App) GetUser() string {
	return string(a.auth.User)
}

func (a *App) Logout() {
	a.Disconnect()
	a.auth = AuthState{}
	a.saveAuth()
}

func (a *App) HideWindow() {
	wailsRT.WindowHide(a.ctx)
}

func (a *App) ShowWindow() {
	wailsRT.WindowShow(a.ctx)
}

func (a *App) CheckUpdate() UpdateInfo {
	resp, err := http.Get("https://api.github.com/repos/674542449/tunnel-pro/releases/latest")
	if err != nil {
		return UpdateInfo{}
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	json.NewDecoder(resp.Body).Decode(&release)
	ver := strings.TrimPrefix(release.TagName, "v")
	if ver != "" && ver != clientVersion {
		dlURL := ""
		for _, asset := range release.Assets {
			if strings.HasSuffix(asset.Name, ".exe") {
				dlURL = asset.BrowserDownloadURL
				break
			}
		}
		return UpdateInfo{Available: true, Version: ver, URL: dlURL}
	}
	return UpdateInfo{}
}

func (a *App) OpenURL(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func (a *App) BuyPlan(planID int64, couponCode string) (string, error) {
	body, _ := json.Marshal(map[string]any{"plan_id": planID, "coupon_code": couponCode})
	req, _ := http.NewRequest("POST", apiBase+"/api/buy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		PayURL  string `json:"pay_url"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return "", errors.New(result.Error)
	}
	if result.PayURL != "" {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", result.PayURL).Start()
		return result.PayURL, nil
	}
	return "", fmt.Errorf(result.Message)
}

func (a *App) checkUpdateOnStart() {
	time.Sleep(2 * time.Second)
	info := a.CheckUpdate()
	if info.Available {
		wailsRT.EventsEmit(a.ctx, "update-available", info)
	}
}

func (a *App) apiGet(path string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", apiBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	return http.DefaultClient.Do(req)
}

func (a *App) GetProfile() (*ProfileInfo, error) {
	resp, err := a.apiGet("/api/me")
	if err != nil {
		return nil, fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		User struct {
			ID        int64  `json:"id"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			Active    bool   `json:"active"`
			ExpiresAt int64  `json:"expires_at"`
			CreatedAt int64  `json:"created_at"`
			TrialUsed bool   `json:"trial_used"`
			PlanID    int64  `json:"plan_id"`
		} `json:"user"`
		Plans []PlanInfo `json:"plans"`
		Error string     `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}

	tResp, err := a.apiGet("/api/my-traffic")
	var upload, download, trafficLimit int64
	if err == nil {
		defer tResp.Body.Close()
		var tr struct {
			Upload       int64 `json:"upload"`
			Download     int64 `json:"download"`
			TrafficLimit int64 `json:"traffic_limit"`
		}
		json.NewDecoder(tResp.Body).Decode(&tr)
		upload = tr.Upload
		download = tr.Download
		trafficLimit = tr.TrafficLimit
	}

	return &ProfileInfo{
		ID:           result.User.ID,
		Email:        result.User.Email,
		Role:         result.User.Role,
		Active:       result.User.Active,
		ExpiresAt:    result.User.ExpiresAt,
		CreatedAt:    result.User.CreatedAt,
		TrialUsed:    result.User.TrialUsed,
		Upload:       upload,
		Download:     download,
		TrafficLimit: trafficLimit,
		PlanID:       result.User.PlanID,
		Plans:        result.Plans,
	}, nil
}

func (a *App) GetOrders() []OrderInfo {
	resp, err := a.apiGet("/api/orders")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Orders []OrderInfo `json:"orders"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Orders
}

func (a *App) ChangePassword(oldPass, newPass string) error {
	body, _ := json.Marshal(map[string]string{"old_password": oldPass, "new_password": newPass})
	req, _ := http.NewRequest("POST", apiBase+"/api/change-password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) CheckEmail(email string) (bool, error) {
	body, _ := json.Marshal(map[string]string{"email": email})
	resp, err := http.Post(apiBase+"/api/check-email", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		Exists bool `json:"exists"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Exists, nil
}

func (a *App) BindEmail(email, pass string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	req, _ := http.NewRequest("POST", apiBase+"/api/bind-email", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("网络错误")
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) GetSavedEmail() string {
	return a.auth.SavedEmail
}

func (a *App) ToggleFavorite(nodeID int64) []int64 {
	found := false
	var newFavs []int64
	for _, id := range a.auth.Favorites {
		if id == nodeID {
			found = true
		} else {
			newFavs = append(newFavs, id)
		}
	}
	if !found {
		newFavs = append(newFavs, nodeID)
	}
	a.auth.Favorites = newFavs
	a.saveAuth()
	return newFavs
}

func (a *App) GetFavorites() []int64 {
	return a.auth.Favorites
}

func (a *App) SetDarkMode(dark bool) {
	a.auth.DarkMode = &dark
	a.saveAuth()
}

func (a *App) GetDarkMode() *bool {
	return a.auth.DarkMode
}

func (a *App) GetVersion() string { return clientVersion }

func (a *App) GetAnnouncements() []AnnouncementInfo {
	resp, err := http.Get(apiBase + "/api/announcements")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Announcements []AnnouncementInfo `json:"announcements"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Announcements
}

func (a *App) addConnLog(action, node, detail string) {
	a.connLogMu.Lock()
	defer a.connLogMu.Unlock()
	entry := ConnLogEntry{Time: time.Now().Unix(), Action: action, Node: node, Detail: detail}
	a.connLog = append(a.connLog, entry)
	if len(a.connLog) > 200 {
		a.connLog = a.connLog[len(a.connLog)-200:]
	}
}

func (a *App) GetConnLog() []ConnLogEntry {
	a.connLogMu.Lock()
	defer a.connLogMu.Unlock()
	out := make([]ConnLogEntry, len(a.connLog))
	copy(out, a.connLog)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) SetKillSwitch(on bool) {
	a.killSwitch = on
	a.auth.KillSwitch = on
	a.saveAuth()
	if on && !a.proxyOn {
		a.enableKillSwitch()
	}
	if !on {
		a.disableKillSwitch()
	}
}

func (a *App) GetKillSwitch() bool {
	return a.killSwitch
}

func (a *App) enableKillSwitch() {
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run := func(args ...string) { exec.Command("reg", args...).Run() }
	run("add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	run("add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", "http=0.0.0.0:1;https=0.0.0.0:1", "/f")
	run("add", regPath, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", "", "/f")
	refreshProxy()
	log.Println("[killswitch] network blocked")
}

func (a *App) disableKillSwitch() {
	clearSystemProxy()
	log.Println("[killswitch] network restored")
}

func (a *App) CopyToClipboard(text string) {
	exec.Command("cmd", "/c", "echo|set /p="+text+"|clip").Run()
}

func (a *App) GetProxyMode() string {
	if a.auth.ProxyMode == "" {
		return "bypass"
	}
	return a.auth.ProxyMode
}

func (a *App) SetProxyMode(mode string) {
	a.auth.ProxyMode = mode
	a.saveAuth()
	if a.proxyOn {
		a.applyProxyMode()
	}
}

func (a *App) applyProxyMode() {
	a.stopTUN()
	clearSystemProxy()
	os.Remove(a.pacPath)

	mode := a.GetProxyMode()
	switch mode {
	case "bypass":
		a.writePAC()
		setSystemProxyPAC(a.pacPath)
	case "tun":
		go a.startTUN()
	default:
		setSystemProxy(a.mixedAddr)
	}
}

func (a *App) writePAC() {
	pac := `function FindProxyOrReturn(url, host) {
    var PROXY = "PROXY 127.0.0.1:7890; SOCKS5 127.0.0.1:7890; DIRECT";
    var DIRECT_VAL = "DIRECT";
    if (isPlainHostName(host) || host === "127.0.0.1" || host === "localhost") return DIRECT_VAL;
    var cnDomains = [
        ".cn", ".com.cn", ".net.cn", ".org.cn",
        ".baidu.com", ".qq.com", ".taobao.com", ".tmall.com", ".jd.com",
        ".alipay.com", ".aliyun.com", ".163.com", ".126.com", ".sina.com.cn",
        ".weibo.com", ".sohu.com", ".youku.com", ".bilibili.com", ".zhihu.com",
        ".douyin.com", ".toutiao.com", ".bytedance.com", ".csdn.net",
        ".douban.com", ".meituan.com", ".pinduoduo.com", ".xiaomi.com",
        ".huawei.com", ".tencent.com", ".wechat.com", ".weixin.qq.com",
        ".sogou.com", ".360.cn", ".iqiyi.com", ".cctv.com",
        ".gov.cn", ".edu.cn", ".mil.cn"
    ];
    for (var i = 0; i < cnDomains.length; i++) {
        if (dnsDomainIs(host, cnDomains[i]) || host === cnDomains[i].substring(1)) return DIRECT_VAL;
    }
    var cnIpRanges = [
        [167772160, 184549375],     // 10.0.0.0/8
        [2886729728, 2887778303],   // 172.16.0.0/12
        [3232235520, 3232301055],   // 192.168.0.0/16
        [16777216, 33554431],       // 1.0.0.0 - 1.255.255.255
        [1946157056, 2013265919],   // 116.0.0.0 - 119.255.255.255
        [2030043136, 2046820351],   // 121.0.0.0 - 121.255.255.255
        [2063597568, 2080374783],   // 123.0.0.0 - 123.255.255.255
        [1811939328, 1879048191],   // 108.0.0.0 - 111.255.255.255
        [3707764736, 3774873599],   // 221.0.0.0 - 224.255.255.255
        [3758096384, 3825205247],   // 224.0.0.0 - 227.255.255.255
        [637534208, 671088639],     // 38.0.0.0 - 39.255.255.255
        [754974720, 788529151],     // 45.0.0.0 - 46.255.255.255
        [3087007744, 3087007744+16777215]  // 184.0.0.0/8
    ];
    if (/^\d+\.\d+\.\d+\.\d+$/.test(host)) {
        var parts = host.split(".");
        var ip = (+parts[0])*16777216 + (+parts[1])*65536 + (+parts[2])*256 + (+parts[3]);
        for (var j = 0; j < cnIpRanges.length; j++) {
            if (ip >= cnIpRanges[j][0] && ip <= cnIpRanges[j][1]) return DIRECT_VAL;
        }
    }
    return PROXY;
}
function FindProxyForURL(url, host) { return FindProxyOrReturn(url, host); }
`
	os.WriteFile(a.pacPath, []byte(pac), 0644)
}

func setSystemProxyPAC(pacPath string) {
	abs, err := filepath.Abs(pacPath)
	if err != nil {
		abs = pacPath
	}
	absSlash := filepath.ToSlash(abs)
	pacURL := "file:///" + absSlash
	log.Printf("[proxy] PAC URL: %s", pacURL)
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run := func(args ...string) { exec.Command("reg", args...).Run() }
	run("add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	run("add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", "", "/f")
	run("add", regPath, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", pacURL, "/f")
	refreshProxy()
}

func (a *App) startTUN() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	t2sPath := filepath.Join(dir, "tun2socks.exe")
	wintunPath := filepath.Join(dir, "wintun.dll")

	if !fileExists(t2sPath) || !fileExists(wintunPath) {
		log.Println("[TUN] downloading components...")
		if err := downloadTUNComponents(dir); err != nil {
			log.Printf("[TUN] download failed: %v, falling back to global", err)
			setSystemProxy(a.mixedAddr)
			return
		}
	}

	gw, err := getDefaultGateway()
	if err != nil {
		log.Printf("[TUN] cannot detect gateway: %v, falling back to global", err)
		setSystemProxy(a.mixedAddr)
		return
	}
	a.origGateway = gw
	log.Printf("[TUN] original gateway: %s", gw)

	a.tunCmd = exec.Command(t2sPath,
		"-device", "wintun://TunnelPro",
		"-proxy", "socks5://127.0.0.1:7890",
		"-loglevel", "warn",
	)
	a.tunCmd.Dir = dir
	a.tunCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := a.tunCmd.Start(); err != nil {
		log.Printf("[TUN] start failed: %v, falling back to global", err)
		setSystemProxy(a.mixedAddr)
		return
	}

	time.Sleep(3 * time.Second)

	exec.Command("netsh", "interface", "ip", "set", "address", "TunnelPro", "static", "10.0.85.2", "255.255.255.0", "10.0.85.1").Run()
	exec.Command("netsh", "interface", "ip", "set", "dns", "TunnelPro", "static", "8.8.8.8").Run()
	exec.Command("netsh", "interface", "ip", "add", "dns", "TunnelPro", "1.1.1.1", "index=2").Run()

	var serverIP string
	a.mu.RLock()
	for _, n := range a.nodes {
		if n.ID == a.activeID {
			serverIP = n.Addr
			if h, _, err := net.SplitHostPort(serverIP); err == nil {
				serverIP = h
			}
			if ips, err := net.LookupHost(serverIP); err == nil && len(ips) > 0 {
				serverIP = ips[0]
			}
			break
		}
	}
	a.mu.RUnlock()

	if serverIP != "" {
		exec.Command("route", "add", serverIP, "mask", "255.255.255.255", gw, "metric", "5").Run()
		log.Printf("[TUN] route: %s via %s", serverIP, gw)
	}
	exec.Command("route", "add", "0.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "6").Run()
	exec.Command("route", "add", "128.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "6").Run()

	a.tunRunning = true
	log.Println("[TUN] started successfully")
}

func (a *App) stopTUN() {
	if !a.tunRunning {
		return
	}
	exec.Command("route", "delete", "0.0.0.0", "mask", "128.0.0.0", "10.0.85.1").Run()
	exec.Command("route", "delete", "128.0.0.0", "mask", "128.0.0.0", "10.0.85.1").Run()

	if a.tunCmd != nil && a.tunCmd.Process != nil {
		a.tunCmd.Process.Kill()
		a.tunCmd.Wait()
		a.tunCmd = nil
	}
	a.tunRunning = false
	log.Println("[TUN] stopped")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func getDefaultGateway() (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1).NextHop").Output()
	if err != nil {
		return "", err
	}
	gw := strings.TrimSpace(string(out))
	if gw == "" || !strings.Contains(gw, ".") {
		return "", errors.New("no gateway found")
	}
	return gw, nil
}

func downloadTUNComponents(dir string) error {
	arch := runtime.GOARCH
	if arch == "" {
		arch = "amd64"
	}

	t2sURL := "https://github.com/xjasonlyu/tun2socks/releases/download/v2.5.2/tun2socks-windows-" + arch + ".zip"
	log.Printf("[TUN] downloading tun2socks from %s", t2sURL)
	if err := downloadAndExtractZip(t2sURL, dir, func(name string) string {
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			return "tun2socks.exe"
		}
		return ""
	}); err != nil {
		return fmt.Errorf("tun2socks download: %w", err)
	}

	wintunURL := "https://www.wintun.net/builds/wintun-0.14.1.zip"
	wantDLL := "wintun/bin/" + arch + "/wintun.dll"
	log.Printf("[TUN] downloading wintun from %s", wintunURL)
	if err := downloadAndExtractZip(wintunURL, dir, func(name string) string {
		if strings.ToLower(filepath.ToSlash(name)) == wantDLL {
			return "wintun.dll"
		}
		return ""
	}); err != nil {
		return fmt.Errorf("wintun download: %w", err)
	}

	return nil
}

func downloadAndExtractZip(zipURL, destDir string, nameMapper func(string) string) error {
	resp, err := http.Get(zipURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	extracted := 0
	for _, f := range zr.File {
		outName := nameMapper(f.Name)
		if outName == "" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outPath := filepath.Join(destDir, outName)
		out, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			return err
		}
		io.Copy(out, rc)
		out.Close()
		rc.Close()
		extracted++
		log.Printf("[TUN] extracted %s", outName)
	}
	if extracted == 0 {
		return errors.New("no matching files in zip")
	}
	return nil
}

func (a *App) GetNodes() []NodeInfo {
	req, _ := http.NewRequest("GET", apiBase+"/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Nodes []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Addr   string `json:"addr"`
			IP     string `json:"ip"`
			PSK    string `json:"psk"`
			Region string `json:"region"`
		} `json:"nodes"`
		SpeedLimit  int `json:"speed_limit"`
		DeviceLimit int `json:"device_limit"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	a.speedLimitMbps = result.SpeedLimit
	a.deviceLimit = result.DeviceLimit

	a.nodes = nil
	var out []NodeInfo
	for _, n := range result.Nodes {
		a.nodes = append(a.nodes, serverConfig{
			Name: n.Name, Addr: n.Addr, IP: n.IP, PSK: n.PSK, ID: n.ID, Region: n.Region,
		})
		flag := "\U0001F310"
		region := strings.ToUpper(strings.TrimSpace(n.Region))
		for code, emoji := range flagMap {
			if strings.Contains(region, code) || strings.EqualFold(region, code) {
				flag = emoji
				break
			}
		}
		regionNames := map[string]string{
			"JP": "Japan", "US": "United States", "DE": "Germany", "SG": "Singapore",
			"KR": "Korea", "HK": "Hong Kong", "TW": "Taiwan", "GB": "United Kingdom",
			"FR": "France", "CA": "Canada", "AU": "Australia",
		}
		for code, name := range regionNames {
			if strings.Contains(region, name) || strings.Contains(strings.ToUpper(n.Name), code) {
				if f, ok := flagMap[code]; ok {
					flag = f
				}
				break
			}
		}
		out = append(out, NodeInfo{ID: n.ID, Name: n.Name, Region: n.Region, Flag: flag})
	}
	return out
}

func genSessionID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func (a *App) apiSession(action, sid string) error {
	body, _ := json.Marshal(map[string]string{"action": action, "session_id": sid})
	req, _ := http.NewRequest("POST", apiBase+"/api/session", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.auth.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

func (a *App) Connect(nodeID int64) error {
	var cfg *serverConfig
	for i := range a.nodes {
		if a.nodes[i].ID == nodeID {
			cfg = &a.nodes[i]
			break
		}
	}
	if cfg == nil {
		return errors.New("node not found")
	}

	a.Disconnect()

	if a.deviceLimit > 0 {
		sid := genSessionID()
		if err := a.apiSession("connect", sid); err != nil {
			return err
		}
		a.sessionID = sid
	}

	uidStr := ""
	if uid := parseJWTUID(a.auth.Token); uid > 0 {
		uidStr = strconv.FormatInt(uid, 10)
	}

	dialFn := func() (*mux.Mux, error) {
		ws, err := dialWS(*cfg)
		if err != nil {
			return nil, err
		}
		mx, err := mux.NewClientMux(ws, cfg.PSK)
		if err != nil {
			ws.Close()
			return nil, err
		}
		if uidStr != "" {
			mx.SendUserID(uidStr)
		}
		return mx, nil
	}

	pool := mux.NewMuxPool(muxPoolSize, dialFn)
	if _, err := pool.Get(); err != nil {
		return err
	}

	a.mu.Lock()
	a.pool = pool
	a.activeID = nodeID
	a.mu.Unlock()

	a.applyProxyMode()
	a.proxyOn = true
	a.connectedAt = time.Now().Unix()

	a.auth.LastNodeID = nodeID
	a.saveAuth()

	a.addConnLog("connect", cfg.Name, "connected to "+cfg.Addr)
	a.updateTrayTooltip(true, cfg.Name)
	wailsRT.EventsEmit(a.ctx, "connection-changed", StatusInfo{Connected: true, NodeName: cfg.Name, NodeID: nodeID, ConnectedAt: a.connectedAt})
	return nil
}

func (a *App) doRelay(stream, conn io.ReadWriteCloser) {
	if a.speedLimitMbps > 0 {
		bytesPerSec := int64(a.speedLimitMbps) * 1024 * 1024 / 8
		relay.RateLimitedRelay(stream, conn, &a.downBytes, &a.upBytes, bytesPerSec)
	} else {
		relay.CountingRelay(stream, conn, &a.downBytes, &a.upBytes)
	}
}

func (a *App) Disconnect() {
	if a.sessionID != "" {
		a.apiSession("disconnect", a.sessionID)
		a.sessionID = ""
	}
	a.mu.Lock()
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
	a.activeID = -1
	a.connectedAt = 0
	a.mu.Unlock()

	a.stopTUN()
	if a.proxyOn {
		if a.killSwitch {
			a.enableKillSwitch()
		} else {
			clearSystemProxy()
		}
		os.Remove(a.pacPath)
		a.proxyOn = false
	}

	a.addConnLog("disconnect", "", "disconnected")
	a.updateTrayTooltip(false, "")
	if a.ctx != nil {
		wailsRT.EventsEmit(a.ctx, "connection-changed", StatusInfo{Connected: false})
	}
}

func (a *App) GetStatus() StatusInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pool == nil || a.activeID < 0 {
		return StatusInfo{Connected: false}
	}
	name := ""
	for _, n := range a.nodes {
		if n.ID == a.activeID {
			name = n.Name
			break
		}
	}
	return StatusInfo{Connected: true, NodeName: name, NodeID: a.activeID, ConnectedAt: a.connectedAt}
}

func (a *App) GetSpeed() SpeedInfo {
	return SpeedInfo{
		Upload:   a.upBytes.Swap(0),
		Download: a.downBytes.Swap(0),
	}
}

func (a *App) TestLatency(nodeID int64) int {
	var cfg *serverConfig
	for i := range a.nodes {
		if a.nodes[i].ID == nodeID {
			cfg = &a.nodes[i]
			break
		}
	}
	if cfg == nil {
		return -1
	}
	addr := cfg.Addr + ":443"
	if cfg.IP != "" {
		addr = cfg.IP + ":443"
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return -1
	}
	conn.Close()
	return int(time.Since(start).Milliseconds())
}

// ---------- proxy ----------

func (a *App) openStream(host string, port uint16) (*mux.Stream, error) {
	a.mu.RLock()
	p := a.pool
	a.mu.RUnlock()
	if p == nil {
		return nil, errors.New("not connected")
	}
	return p.OpenStream(host, port)
}

func (a *App) startMixed() {
	ln, err := net.Listen("tcp", a.mixedAddr)
	if err != nil {
		log.Printf("[mixed] listen error: %v", err)
		return
	}
	log.Printf("[mixed] listening on %s", a.mixedAddr)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go a.handleMixed(c)
	}
}

type peekConn struct {
	net.Conn
	peeked []byte
	idx    int
}

func (c *peekConn) Read(b []byte) (int, error) {
	if c.idx < len(c.peeked) {
		n := copy(b, c.peeked[c.idx:])
		c.idx += n
		return n, nil
	}
	return c.Conn.Read(b)
}

func (a *App) handleMixed(conn net.Conn) {
	first := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := conn.Read(first)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	wrapped := &peekConn{Conn: conn, peeked: first}
	if first[0] == 0x05 {
		socks5.HandleConn(wrapped, func(c net.Conn, host string, port uint16) {
			defer c.Close()
			stream, err := a.openStream(host, port)
			if err != nil {
				socks5.ReplyFailure(c)
				return
			}
			defer stream.Close()
			socks5.ReplySuccess(c)
			a.doRelay(stream, c)
		})
	} else {
		a.handleHTTP(wrapped)
	}
}

func (a *App) handleHTTP(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		host, portStr, err := net.SplitHostPort(req.Host)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		port, _ := strconv.Atoi(portStr)
		stream, err := a.openStream(host, uint16(port))
		if err != nil {
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer stream.Close()
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		a.doRelay(stream, conn)
		return
	}
	host := req.URL.Hostname()
	port := uint16(80)
	if req.URL.Port() != "" {
		p, _ := strconv.Atoi(req.URL.Port())
		port = uint16(p)
	}
	stream, err := a.openStream(host, port)
	if err != nil {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer stream.Close()
	req.RequestURI = req.URL.RequestURI()
	req.Header.Del("Proxy-Connection")
	var buf bytes.Buffer
	req.Write(&buf)
	stream.Write(buf.Bytes())
	a.doRelay(stream, conn)
}

func (a *App) healthCheck() {
	for {
		time.Sleep(5 * time.Second)
		a.mu.RLock()
		p := a.pool
		id := a.activeID
		a.mu.RUnlock()
		if p == nil || id < 0 {
			continue
		}
		if !p.IsClosed() {
			continue
		}
		a.addConnLog("lost", "", "connection lost, attempting reconnect")
		log.Println("[health] connection lost, reconnecting...")
		if a.ctx != nil {
			wailsRT.EventsEmit(a.ctx, "reconnecting", true)
		}
		ok := false
		for retry := 0; retry < 5; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(2<<retry) * time.Second)
			}
			if _, err := p.Get(); err != nil {
				log.Printf("[health] retry %d failed: %v", retry+1, err)
				continue
			}
			ok = true
			log.Println("[health] reconnected")
			break
		}
		if a.ctx != nil {
			wailsRT.EventsEmit(a.ctx, "reconnecting", false)
		}
		if ok {
			a.addConnLog("reconnect", "", "reconnected successfully")
		} else {
			a.addConnLog("failed", "", "reconnect failed after 5 attempts")
			a.Disconnect()
		}
	}
}

// ---------- system proxy ----------

func setSystemProxy(addr string) {
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run := func(args ...string) { exec.Command("reg", args...).Run() }
	run("add", regPath, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", "", "/f")
	run("add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	run("add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d",
		"http="+addr+";https="+addr+";socks="+addr, "/f")
	run("add", regPath, "/v", "ProxyOverride", "/t", "REG_SZ", "/d",
		"localhost;127.*;10.*;192.168.*;<local>", "/f")
	log.Printf("[proxy] system proxy set to %s", addr)
	refreshProxy()
}

func clearSystemProxy() {
	regPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	run := func(args ...string) { exec.Command("reg", args...).Run() }
	run("add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	run("add", regPath, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", "", "/f")
	refreshProxy()
}

func refreshProxy() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	set := wininet.NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0)
	set.Call(0, 37, 0, 0)
}

// ---------- websocket dial ----------

func dialWS(cfg serverConfig) (*websocket.Conn, error) {
	wsPath := proto.DynamicPath(cfg.PSK, 0)
	remoteURL := "wss://" + cfg.Addr + wsPath
	u, _ := url.Parse(remoteURL)
	serverHost := u.Hostname()
	serverPort := u.Port()
	if serverPort == "" {
		serverPort = "443"
	}
	dialAddr := serverHost + ":" + serverPort
	if cfg.IP != "" {
		dialAddr = cfg.IP + ":" + serverPort
	}
	dialer := &websocket.Dialer{
		Proxy:            func(*http.Request) (*url.URL, error) { return nil, nil },
		HandshakeTimeout: 15 * time.Second,
		ReadBufferSize:   mux.WsBufSize(),
		WriteBufferSize:  mux.WsBufSize(),
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			tcpConn, err := net.DialTimeout(network, dialAddr, 15*time.Second)
			if err != nil {
				return nil, err
			}
			if tc, ok := tcpConn.(*net.TCPConn); ok {
				tc.SetNoDelay(true)
				tc.SetReadBuffer(256 * 1024)
				tc.SetWriteBuffer(256 * 1024)
			}
			tlsCfg := &utls.Config{ServerName: serverHost}
			spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
			if err != nil {
				tcpConn.Close()
				return nil, err
			}
			for _, ext := range spec.Extensions {
				if alpn, ok := ext.(*utls.ALPNExtension); ok {
					alpn.AlpnProtocols = []string{"http/1.1"}
					break
				}
			}
			tlsConn := utls.UClient(tcpConn, tlsCfg, utls.HelloCustom)
			if err := tlsConn.ApplyPreset(&spec); err != nil {
				tcpConn.Close()
				return nil, err
			}
			if err := tlsConn.Handshake(); err != nil {
				tcpConn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}
	headers := http.Header{}
	headers.Set("User-Agent", uaPool[rand.Intn(len(uaPool))])
	ws, _, err := dialer.Dial(remoteURL, headers)
	return ws, err
}
