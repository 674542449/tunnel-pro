package main

import (
	_ "embed"

	"github.com/getlantern/systray"
	wailsRT "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/tray.ico
var trayIcon []byte

func (a *App) startTray() {
	systray.Run(a.onTrayReady, func() {})
}

func (a *App) onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("Tunnel Pro")
	systray.SetTooltip("Tunnel Pro - 未连接")

	mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
	systray.AddSeparator()
	mDisconnect := systray.AddMenuItem("断开连接", "断开当前节点")
	mDisconnect.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 Tunnel Pro")

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
		systray.SetTooltip("Tunnel Pro - 已连接: " + nodeName)
		if a.trayDisconnect != nil {
			a.trayDisconnect.Enable()
		}
	} else {
		systray.SetTooltip("Tunnel Pro - 未连接")
		if a.trayDisconnect != nil {
			a.trayDisconnect.Disable()
		}
	}
}
