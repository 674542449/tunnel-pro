//go:build windows

package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"tunnelx/internal/control"
	"tunnelx/internal/desktop"
	"tunnelx/internal/systemproxy"
)

//go:embed ui/*
var assets embed.FS

//go:embed build/windows/tray.ico
var icon []byte

type App struct {
	engine            *desktop.Engine
	ctx               context.Context
	dataDir, dataMode string
}

func (a *App) Status() map[string]any {
	status := a.engine.Status()
	status["data_directory"], status["data_mode"] = a.dataDir, a.dataMode
	return status
}
func (a *App) Login(email, password string, register bool) error {
	return a.engine.Login(email, password, register)
}
func (a *App) LoginSecure(email, password, code, invite string, register bool) error {
	return a.engine.LoginSecure(email, password, code, invite, register)
}
func (a *App) OpenPortal()   { rt.BrowserOpenURL(a.ctx, a.engine.PortalURL()) }
func (a *App) Logout() error { return a.engine.Logout() }

// A concrete nullable type is required at the Wails reflection boundary.
// JSON null decoded into any becomes an invalid reflect.Value and never
// reaches the method; a typed nil map remains a valid argument.
func (a *App) Query(path string, body map[string]any) (any, error) {
	if body == nil {
		return a.engine.Query(path, nil)
	}
	return a.engine.Query(path, body)
}
func (a *App) Connect(id string, proxy bool) error { return a.engine.Connect(id, proxy) }
func (a *App) Disconnect() error                   { return a.engine.Disconnect() }
func (a *App) RecoverProxy() error                 { return a.engine.RecoverProxy() }
func (a *App) Probe(id string) (int64, error)      { return a.engine.Probe(id) }
func (a *App) ProbeAll() ([]desktop.ProbeResult, error) { return a.engine.ProbeAll() }
func (a *App) ConnLogs() []desktop.ConnLog           { return a.engine.ConnLogs() }
func (a *App) SetAPI(base string) error            { return a.engine.SetAPI(base) }
func (a *App) SavePreferences(nodeID string, proxy bool) error {
	return a.engine.SavePreferences(nodeID, proxy)
}
func (a *App) SaveProxyMode(mode, exceptions string) error {
	return a.engine.SaveProxyMode(mode, exceptions)
}
func (a *App) UpdateRoutingRules() error          { return a.engine.UpdateRoutingRules() }
func (a *App) CancelConnect() error               { return a.engine.CancelConnect() }
func (a *App) DiagnosticsReport() (string, error) { return a.engine.DiagnosticsReport() }
func (a *App) OpenDownload() error {
	commerce, err := a.engine.Query("/api/public/commerce", nil)
	if err != nil {
		return err
	}
	url, err := publicReleaseDownloadURL(commerce)
	if err != nil {
		return err
	}
	rt.BrowserOpenURL(a.ctx, url)
	return nil
}
func (a *App) ExportDiagnostics() (string, error) {
	path, err := rt.SaveFileDialog(a.ctx, rt.SaveDialogOptions{
		Title: "导出脱敏诊断报告", DefaultFilename: "tunnelX-diagnostics-" + time.Now().Format("20060102-150405") + ".txt",
		Filters: []rt.FileFilter{{DisplayName: "文本文件 (*.txt)", Pattern: "*.txt"}}, CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return "", err
	}
	report, err := a.engine.DiagnosticsReport()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("请选择完整的报告保存路径")
	}
	if err = control.WriteFile(path, []byte(report)); err != nil {
		return "", fmt.Errorf("诊断报告保存失败，请检查目录权限或选择其他位置")
	}
	return path, nil
}
func (a *App) Minimize() { rt.WindowHide(a.ctx) }
func (a *App) SetTheme(theme string) error {
	if theme != "system" && theme != "light" && theme != "dark" {
		return fmt.Errorf("不支持的颜色主题")
	}
	if a.ctx == nil {
		return fmt.Errorf("窗口尚未就绪")
	}
	switch theme {
	case "light":
		rt.WindowSetLightTheme(a.ctx)
	case "dark":
		rt.WindowSetDarkTheme(a.ctx)
	default:
		rt.WindowSetSystemDefaultTheme(a.ctx)
	}
	return nil
}
func (a *App) Quit() error {
	if err := a.engine.Disconnect(); err != nil {
		return err
	}
	a.rememberWindow()
	rt.Quit(a.ctx)
	return nil
}
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if loadWindowState(a.dataDir).Maximised {
		rt.WindowMaximise(ctx)
	}
	maximiseOnSmallScreen(ctx)
	// Wails has acquired its single-instance lock before startup. Any recovery
	// failure stays visible in Status and can be retried without ending the app.
	a.engine.RecoverProxy()
	go systray.Run(func() {
		systray.SetIcon(icon)
		systray.SetTitle("tunnelX")
		systray.SetTooltip("tunnelX · HTTP/2")
		show := systray.AddMenuItem("显示窗口", "")
		disconnect := systray.AddMenuItem("断开节点", "")
		quit := systray.AddMenuItem("退出", "")
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-show.ClickedCh:
					rt.WindowShow(ctx)
				case <-disconnect.ClickedCh:
					if err := a.engine.Disconnect(); err != nil {
						rt.WindowShow(ctx)
					}
				case <-quit.ClickedCh:
					if err := a.Quit(); err != nil {
						rt.WindowShow(ctx)
					} else {
						return
					}
				}
			}
		}()
	}, func() {})
}

func maximiseOnSmallScreen(ctx context.Context) {
	// Wails creates the native window before OnStartup and allows maximising
	// before its first show. Size uses logical pixels, including DPI scaling.
	screens, err := rt.ScreenGetAll(ctx)
	if err != nil {
		return
	}
	for _, current := range []bool{true, false} {
		for _, screen := range screens {
			if current && screen.IsCurrent || !current && screen.IsPrimary {
				width, height := rt.WindowGetSize(ctx)
				if shouldMaximise(screen.Size.Width, screen.Size.Height) || width+40 > screen.Size.Width || height+72 > screen.Size.Height {
					rt.WindowMaximise(ctx)
				}
				return
			}
		}
	}
}

var headlessErrors bool

func main() {
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法确定程序所在位置")
	}
	path := flag.String("config", "", "optional desktop settings path")
	headless := flag.Bool("headless", false, "acceptance control API without rendering a window")
	stateDir := flag.String("state-dir", "", "isolated writable desktop data directory")
	flag.Parse()
	headlessErrors = *headless
	paths, err := resolveDesktopPaths(filepath.Dir(exe), os.Getenv("LOCALAPPDATA"), *path, *stateDir)
	if err != nil {
		return err
	}
	settings, err := readDesktopSettings(paths)
	if err != nil {
		return err
	}
	engine, e := desktop.New(paths.Root, settings)
	if e != nil {
		return e
	}
	defer engine.Close()
	app := &App{engine: engine, dataDir: paths.Root, dataMode: paths.Mode}
	if *headless {
		return serveHeadless(app, settings.Web)
	}
	ui, err := fs.Sub(assets, "ui")
	if err != nil {
		return fmt.Errorf("客户端界面资源无法读取，请重新下载完整客户端")
	}
	messages := windows.DefaultMessages()
	messages.MissingRequirements, messages.Error = "tunnelX · 缺少运行环境", "tunnelX · 启动失败"
	messages.Webview2NotInstalled = "Microsoft Edge WebView2 运行时尚未安装或需要更新"
	messages.ContactAdmin = "tunnelX 需要 Microsoft Edge WebView2 Runtime。\n请从微软官网下载 Evergreen Runtime 安装程序，安装后重新打开 tunnelX。\n" + webView2DownloadURL
	messages.WebView2ProcessCrash = "界面运行环境意外退出，请重新打开 tunnelX；如果反复发生，请更新 Microsoft Edge WebView2 Runtime。"
	geometry := loadWindowState(paths.Root)
	e = wails.Run(&options.App{Title: "tunnelX", Width: geometry.Width, Height: geometry.Height, MinWidth: 700, MinHeight: 560, AssetServer: &assetserver.Options{Assets: ui}, BackgroundColour: &options.RGBA{R: 243, G: 244, B: 246, A: 255}, OnStartup: app.startup, OnBeforeClose: func(context.Context) bool { app.rememberWindow(); return false }, OnShutdown: func(context.Context) { engine.Close(); systray.Quit() }, HideWindowOnClose: true, Bind: []interface{}{app}, SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "fbb4ebcb-637d-43cd-9e27-50b3dd05d094", OnSecondInstanceLaunch: func(options.SecondInstanceData) {
		if app.ctx != nil {
			rt.WindowShow(app.ctx)
		}
	}}, Windows: &windows.Options{Theme: windows.SystemDefault, Messages: messages, WebviewUserDataPath: filepath.Join(paths.Root, "webview2")}})
	return e
}
func fatal(e error) {
	message := startupFailureMessage(e)
	fmt.Fprintln(os.Stderr, message)
	if !headlessErrors {
		systemproxy.ShowError(message)
	}
	os.Exit(1)
}
