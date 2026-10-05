package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"tunnelx/internal/config"
	"tunnelx/internal/desktop"
)

const defaultDesktopAPI = "https://test.xiaguamail.com/control"
const webView2DownloadURL = "https://developer.microsoft.com/en-us/microsoft-edge/webview2/"

func shouldMaximise(width, height int) bool {
	return width > 0 && height > 0 && (width < 1000 || height < 760)
}

type desktopPaths struct {
	Root, Config, Mode string
	ExplicitConfig     bool
}

// Existing portable state stays in place; nothing is copied from or scanned in
// other installations. An explicit test/portable root never probes legacy data.
func resolveDesktopPaths(exeDir, localAppData, configPath, stateDir string) (desktopPaths, error) {
	p := desktopPaths{ExplicitConfig: configPath != ""}
	var err error
	if stateDir != "" {
		p.Root, err = filepath.Abs(stateDir)
		p.Mode = "portable"
	} else {
		info, statErr := os.Stat(filepath.Join(exeDir, "state"))
		if statErr != nil && !os.IsNotExist(statErr) {
			return p, errors.New("无法检查旧版本本机数据目录，请检查目录权限或指定 -state-dir")
		}
		if statErr == nil && info.IsDir() {
			p.Root, err = filepath.Abs(exeDir)
			p.Mode = "legacy-portable"
		} else {
			if strings.TrimSpace(localAppData) == "" {
				return p, errors.New("无法确定 Windows 本机应用数据目录，请检查 LOCALAPPDATA 或使用 -state-dir 指定可写位置")
			}
			p.Root, err = filepath.Abs(filepath.Join(localAppData, "tunnelXDesktop"))
			p.Mode = "appdata"
		}
	}
	if err != nil {
		return p, errors.New("本机数据目录无效")
	}
	if configPath != "" {
		p.Config, err = filepath.Abs(configPath)
		if err != nil {
			return p, errors.New("配置文件路径无效")
		}
		return p, nil
	}
	p.Config = filepath.Join(p.Root, "settings.json")
	if p.Mode == "appdata" {
		if _, err = os.Stat(p.Config); os.IsNotExist(err) {
			bundled := filepath.Join(exeDir, "settings.json")
			if info, statErr := os.Stat(bundled); statErr == nil && info.Mode().IsRegular() {
				// A bundled server address is a bootstrap setting, never a reason
				// to store credentials next to a new installation's executable.
				p.Config = bundled
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return p, errors.New("无法读取随客户端提供的配置文件")
			}
		} else if err != nil {
			return p, errors.New("无法读取本机客户端配置文件")
		}
	}
	return p, nil
}

func readDesktopSettings(paths desktopPaths) (desktop.Settings, error) {
	settings := desktop.Settings{APIURL: defaultDesktopAPI}
	if err := config.Read(paths.Config, &settings); err != nil {
		if os.IsNotExist(err) && !paths.ExplicitConfig {
			return settings, nil
		}
		if os.IsNotExist(err) {
			return settings, errors.New("指定的配置文件不存在，请检查 -config 路径")
		}
		return settings, fmt.Errorf("客户端配置文件无法读取或格式不正确，请修复 settings.json 后重试：%w", err)
	}
	if err := desktop.ValidateURL(settings.APIURL); err != nil {
		return settings, err
	}
	return settings, nil
}

func publicReleaseDownloadURL(value any) (string, error) {
	commerce, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("下载信息格式无效，请稍后重试")
	}
	return releaseDownloadURL(commerce["release"])
}

func releaseDownloadURL(value any) (string, error) {
	release, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("下载信息格式无效，请稍后重试")
	}
	raw, _ := release["url"].(string)
	if raw == "" {
		return "", errors.New("暂未发布客户端下载地址")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("下载地址无效：仅允许不含登录凭据的 HTTPS 链接")
	}
	return u.String(), nil
}

func startupFailureMessage(err error) string {
	message := "tunnelX 启动失败。\n"
	if err != nil {
		message += err.Error() + "\n"
	}
	return message + "\n如果提示 WebView2 缺失或界面运行环境异常，请从微软官网下载并安装 Evergreen Runtime，然后重新打开客户端：\n" + webView2DownloadURL + "\n\n其他配置或目录错误请检查 settings.json 和本机数据目录的读写权限。"
}
