package main

import (
	"os"
	"path/filepath"

	"github.com/getlantern/systray"
	wailsRT "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) startTray() {
	systray.Run(a.onTrayReady, func() {})
}

func (a *App) onTrayReady() {
	iconData := loadTrayIcon()
	if iconData != nil {
		systray.SetIcon(iconData)
	}
	systray.SetTitle("Tunnel Pro")
	systray.SetTooltip("Tunnel Pro - Disconnected")

	mShow := systray.AddMenuItem("Show", "Show window")
	systray.AddSeparator()
	mDisconnect := systray.AddMenuItem("Disconnect", "Disconnect from node")
	mDisconnect.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Tunnel Pro")

	a.trayDisconnect = mDisconnect

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				wailsRT.WindowShow(a.ctx)
				wailsRT.WindowSetAlwaysOnTop(a.ctx, true)
				wailsRT.WindowSetAlwaysOnTop(a.ctx, false)
			case <-mDisconnect.ClickedCh:
				a.Disconnect()
			case <-mQuit.ClickedCh:
				a.Disconnect()
				systray.Quit()
				wailsRT.Quit(a.ctx)
			}
		}
	}()
}

func (a *App) updateTrayTooltip(connected bool, nodeName string) {
	if connected {
		systray.SetTooltip("Tunnel Pro - " + nodeName)
		if a.trayDisconnect != nil {
			a.trayDisconnect.Enable()
		}
	} else {
		systray.SetTooltip("Tunnel Pro - Disconnected")
		if a.trayDisconnect != nil {
			a.trayDisconnect.Disable()
		}
	}
}

func loadTrayIcon() []byte {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	// try tray.ico next to exe first, then in build/windows
	for _, p := range []string{
		filepath.Join(dir, "tray.ico"),
		filepath.Join(dir, "..", "..", "build", "windows", "tray.ico"),
	} {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}
