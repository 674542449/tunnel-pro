# 后台与 Windows EXE 统一发布规范

根目录 `VERSION` 是产品版本的唯一来源。从 v0.6.4 起，管理后台、后台页面、Windows EXE 内的版本、客户端页面、安装包和网站公布的下载版本必须一致。网络内核的 `control.Version` 是另一层协议版本，升级产品时不能直接改它。

## 每次修改与发布

1. 检查后台接口、Windows 调用、用户权限和错误处理是否同时适配。版本相同不替代功能与接口测试。
2. 修改 `VERSION`，运行 `python tools/versioning.py --sync`。保留历史验收报告原有版本，不全目录替换版本字符串。
3. `powershell -File tools/build-release.ps1 -Python python` 同时构建两端，执行版本与发布门禁的正反例回归。单独构建也会校验两端源文件及页面版本，不一致立即失败。需要 Go、Wails、Node 和 Python；race 测试还需要 C 编译器。
4. 执行相关 Go/API 回归、`node tools/test-desktop-product.mjs`、`node tools/test-desktop-runtime.mjs`，并对新 EXE 执行 `tools/accept-desktop-product.py` 的隔离原生验收。该验收需要私有测试服务；不得把凭据写入公开包。从 v0.6.7 起，桌面构建自动执行 `python tools/test-desktop-binding.py`，使用实际 App 方法经过 Wails 原生参数解析、反射和回调分发；覆盖 `null` 读取、对象写入和异常参数。打包校验该报告与源文件哈希，不能仅用模拟回调或 headless 测试替代。
5. 为该版本编写 Windows 使用说明，运行 `python tools/package-control.py --with-desktop`。桌面包统一走原生 EXE 哈希、UI、Wails runtime 和敏感数据检查，禁止绕开桌面验收重新打包。
6. 先备份，部署验收过的后台和 Windows 下载包，更新正式站与测试站的发布信息。部署失败应回滚，不标记发布成功。保留旧包用于回退，不替换正在运行的用户客户端。
7. 最后运行 `python tools/verify-release.py --site https://test.xiaguamail.com/control --site https://test.xiaguamail.com/beta`。检查两站 `/health`、发布元数据、HTTPS 实际下载 SHA256 与验收 EXE。只有成功生成本版本的 `docs/unified-release-<VERSION>.json` 才算完成上线。

构建或发布时不要手改一端常量，也不要继续执行历史 `.local` 脚本发布旧版本。发布验收只证明检查时的部署状态；之后管理员手动改配置仍需重新运行校验。

## 已安装版本

统一的是服务器当前版本与官方提供的 EXE 版本。已安装旧版本的用户通过“检查发布版本”下载，退出旧程序后更新。目前没有静默自动升级，不能声称用户电脑上的旧进程已经更新。升级期间保持旧客户端接口兼容，避免因后台先重启而断开正常使用。
