# 管理后台 v0.4.2 界面改版

线上地址：https://test.xiaguamail.com/control/ 。刷新页面即可使用新版；客户端和 H2 节点仍为 v0.4.0。

本次按用户指定的 Claude 风格方向重做管理界面：暖白纸面、浅色侧栏、陶土色操作按钮、衬线标题、细边框与克制的留白。保留 tunnelX 自己的名称和原创连接标识。

## 页面与 SVG

- 登录页：独立的双栏布局、原创 SVG 网络插画、带图标的输入框，以及明确的登录/注册入口。手机窄屏使用单栏。
- 总览：真实账号与节点统计、账号到期和累计用量、有限额度进度条、快捷入口与公告。普通用户仅显示自己的授权，不显示管理员统计。
- 侧栏：统一线条图标、当前页面标记、工作空间信息。窄屏使用横向导航，较矮窗口压缩间距并允许侧栏滚动。
- 管理表格：统一标题、记录数、搜索、分页、状态标签和操作图标。长 ID 保留完整内容，用视觉省略减少拥挤；鼠标悬停可查看全文。
- 设置与弹窗：设置状态分组展示；表单按内容分栏，证书与 ECH 等长字段占整行，窄屏使用单栏。
- SVG：`icons.svg` 包含 33 个图标；`network.svg` 是网络插画；`mark.svg` 是独立站点图标。全部随 Go 程序嵌入，由本站提供，不依赖图标 CDN、远端字体或图片服务。

图标作为装饰设置 `aria-hidden`，按钮保留文字。导航设置 `aria-current`，表格列使用语义表头，弹窗关联标题和说明；键盘焦点可见，并尊重系统的减少动态效果设置。主要按钮、正文、表格正文和指定辅助文字的抽样对比度分别为 4.91、12.65、5.51、5.06；这是抽样检查，不是全站无障碍认证。

## 同时修复

新增 SVG 最初会被旧路由当成鉴权接口，返回 401。已将这三个公开资源加入明确的静态资源列表，并校验 `image/svg+xml` 类型；API 权限规则继续保留。

首次打开未登录页面时，不再误显示“登录失效”。已登录期间会话失效，仍会清理界面并提示重新登录。

## 验证范围

- 21 项真实管理 API + DOM 功能回归通过，覆盖 SVG 资源、导航、真实统计、管理员操作、普通用户注册/试用、表单、配置 ZIP、订单、异步页面、登录失效及错误提示。
- Go 管理回归与 go vet 通过；Ubuntu 26.04 ARM64、Debian 13.7 ARM64 各 11 项管理测试通过。Debian 使用独立根文件系统，与 Ubuntu 共享内核。
- CSS 解析通过，检查登录/已登录状态样式、响应式断点和减少动态效果规则；没有浏览器布局测量或像素截图。
- 线上 6 个页面资源与本地发布源码逐字节一致，SVG 类型及 XML 正常；8 个管理读取接口返回正常。
- 控制服务升级保留原有用户、节点、订单和设置；原 H2 节点 PID、重启次数与二进制保持一致，心跳正常，升级后没有新增 panic。

证据：`admin-dom-acceptance-v0.4.2.json`、`admin-design-audit-v0.4.2.json`、`admin-design-live-v0.4.2.json`、`admin-regression-ubuntu-v0.4.2.txt`、`admin-regression-debian-v0.4.2.txt`、`admin-deployment-v0.4.2.json`、`admin-final-health-v0.4.2.json`。

本次没有启动浏览器或渲染页面，视觉效果、真实浏览器断点布局及下载提示尚未实测。Linux amd64 已交叉编译，未在独立机器运行。

## 构建和交付

```powershell
.\tools\build-control.ps1
npm ci --prefix tools/admin-ui-test --ignore-scripts --no-audit --no-fund
node tools/test-admin-ui.mjs
python tools/package-control.py
```

构建使用 Go 1.27、Node.js 24.18.0。管理更新包为 `dist/tunnelX-control-v0.4.2.zip`，包含 Windows x64、Linux arm64/amd64 管理程序、管理安装脚本和许可证。完整源码为 `dist/tunnelX-source-v0.4.2.zip`，校验清单为 `dist/SHA256-control-v0.4.2.json`。发布包排除登录状态、配置凭据、私钥和运行日志。

Linux 管理服务安装沿用 `sudo sh deploy/install-control.sh https://YOUR_DOMAIN/control`。不要为了管理界面升级重装节点或重新初始化管理数据。v0.4.1 的功能修复和既有功能范围见 [上一版记录](ADMIN-FIXES-v0.4.1.md)。
