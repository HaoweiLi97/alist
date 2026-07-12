# AList Windows Desktop Host

This is the lightweight Windows desktop host for AList. It does not embed a
browser view; it only starts the bundled `alist.exe`, keeps a tray icon alive,
and opens the AList web UI in the user's default browser.

## Behavior

- Stores runtime data in `%APPDATA%\AListDesktop\data`.
- Stores desktop and AList logs in `%APPDATA%\AListDesktop\logs`.
- Starts AList on the first available port in `5244-5264`.
- Double-clicking the tray icon opens the current service URL in the default
  browser.
- The tray menu can restart the service, open data/log folders, toggle LAN
  access, toggle launch at login, and quit.

## Development

Build the host only:

```powershell
dotnet build .\desktop\windows\AListDesktop.Windows.csproj
```

Run against an existing AList binary:

```powershell
$env:ALIST_DESKTOP_ALIST_BINARY = "C:\path\to\alist.exe"
dotnet run --project .\desktop\windows\AListDesktop.Windows.csproj
```

Build a packaged publish directory:

```powershell
.\scripts\build-alist-desktop-windows.ps1 -Runtime win-x64
```

The packaged icon and its SVG source live in `desktop/windows/Resources/`.
