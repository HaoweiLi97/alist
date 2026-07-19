# AList Desktop for macOS

菜单栏常驻版 AList 宿主应用放在这个目录，采用原生 Swift 宿主 + 内嵌 `alist` 二进制的方式运行。

## 功能

- 菜单栏常驻，隐藏 Dock 图标
- 后台托管 AList 服务
- 独立窗口内嵌 `WKWebView`
- 数据目录固定在 `~/Library/Application Support/AListDesktop`
- 生成 universal `.app` 与 `.dmg`
- 首次启动会为管理员账号 `admin` 生成随机密码，并在本机显示一次；可通过环境变量 `ALIST_ADMIN_PASSWORD` 指定密码
- 应用图标使用 `desktop/windows/Resources/logo.svg`
- 菜单栏图标使用同一 logo 的 template 版本，macOS 会自动渲染成白色/深色
- 默认仅监听 `127.0.0.1`；启用 LAN 访问前会显示安全提醒

## 本地构建

```bash
./scripts/build-alist-desktop-macos.sh
```

构建产物默认输出到 `desktop/macos/build/`，包括 Universal `.app.zip` 和 `.dmg`。

## 可选签名与公证

脚本支持以下环境变量：

- `ALIST_DESKTOP_APP_NAME`
- `ALIST_DESKTOP_BUNDLE_ID`
- `ALIST_DESKTOP_VERSION`
- `ALIST_DESKTOP_CODESIGN_IDENTITY`
- `ALIST_DESKTOP_NOTARY_PROFILE`
- `ALIST_DESKTOP_NOTARY_APPLE_ID`
- `ALIST_DESKTOP_NOTARY_TEAM_ID`
- `ALIST_DESKTOP_NOTARY_PASSWORD`

有签名证书时：

```bash
ALIST_DESKTOP_CODESIGN_IDENTITY="Developer ID Application: Example (TEAMID)" \
./scripts/build-alist-desktop-macos.sh
```
