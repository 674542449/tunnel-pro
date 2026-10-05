package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"
	"tunnelx/internal/client"
	"tunnelx/internal/config"
	"tunnelx/internal/diagnostics"
	"tunnelx/internal/systemproxy"
)

func main() {
	exe, _ := os.Executable()
	base := filepath.Dir(exe)
	path := flag.String("config", filepath.Join(base, "client.json"), "configuration path")
	noBrowser := flag.Bool("no-browser", false, "do not open the control panel")
	restore := flag.Bool("restore-proxy", false, "restore saved system proxy state then exit")
	logDir := flag.String("log-dir", filepath.Join(base, "logs"), "rolling metadata diagnostics directory")
	healthInterval := flag.Duration("health-interval", 30*time.Second, "independent TCP/HTTP2 health check interval")
	flag.Parse()
	state := filepath.Join(base, "state", "system-proxy.json")
	pm := &systemproxy.Manager{Path: state}
	if *restore {
		if e := pm.Restore(); e != nil {
			fatal(e, *noBrowser)
		}
		return
	}
	var c config.Client
	if e := config.Read(*path, &c); e != nil {
		fatal(e, *noBrowser)
	}
	c.CAFile = config.Resolve(*path, c.CAFile)
	m, e := client.New(c)
	if e != nil {
		fatal(e, *noBrowser)
	}
	if *healthInterval < time.Second || *healthInterval > 5*time.Minute {
		fatal(errors.New("health-interval must be between 1s and 5m"), *noBrowser)
	}
	logger, e := diagnostics.New(*logDir, diagnostics.Options{})
	if e != nil {
		fatal(fmt.Errorf("cannot start persistent diagnostics: %w", e), *noBrowser)
	}
	m.SetDiagnostics(logger)
	defer logger.Close()
	p := client.NewProxy(m)
	if e = p.Start(); e != nil {
		logger.Record("startup_failed", map[string]any{"scope": "client", "stage": "proxy_bind", "error_kind": diagnostics.ErrorKind(e)})
		logger.Close()
		fatal(e, *noBrowser)
	}
	defer func() {
		p.Close()
		m.FinishDiagnostics()
		logger.Record("client_stopped", map[string]any{"scope": "client", "reason": "requested_shutdown"})
	}()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	defer pm.Restore()
	pm.Proxy = m.Config.HTTPListen
	ui := &client.UI{Mux: m, Proxy: pm, Shutdown: cancel}
	if e = ui.Start(); e != nil {
		logger.Record("startup_failed", map[string]any{"scope": "client", "stage": "ui_bind", "error_kind": diagnostics.ErrorKind(e)})
		logger.Close()
		fatal(e, *noBrowser)
	}
	defer ui.HTTP.Close()
	// A duplicate process must fail to bind before touching an active process's
	// saved system proxy state. Restore crash state only after all ports are ours.
	if e = pm.Restore(); e != nil {
		logger.Record("startup_failed", map[string]any{"scope": "client", "stage": "proxy_restore", "error_kind": diagnostics.ErrorKind(e)})
		logger.Close()
		fatal(e, *noBrowser)
	}
	logger.Record("client_started", map[string]any{"scope": "client", "client_version": "v0.4.0", "pid": os.Getpid(), "server": m.Config.ServerIP, "server_port": m.Config.Port, "mode": m.Mode(), "privacy": m.Config.Privacy, "h2_connections": m.Config.H2Connections, "health_interval_seconds": healthInterval.Seconds(), "retention_max_bytes": 16 * 16 << 20})
	finishMonitoring := m.RunDiagnostics(ctx, *healthInterval)
	defer finishMonitoring()
	fmt.Println("tunnelX local proxies ready; control panel: http://" + m.Config.WebListen)
	if !*noBrowser {
		systemproxy.OpenBrowser("http://" + m.Config.WebListen)
	}
	<-ctx.Done()
}
func fatal(e error, headless bool) {
	var listen *net.OpError
	if errors.As(e, &listen) && listen.Op == "listen" {
		e = fmt.Errorf("无法监听本地端口 %v。\n请先在旧版 tunnelX 控制面板点击“退出”，再启动新版；若仍失败，请检查其他程序是否占用该端口。\n\n详情：%w", listen.Addr, e)
	}
	log.Print(e)
	if !headless {
		systemproxy.ShowError(e.Error())
	}
	os.Exit(1)
}
