# AList Desktop

This directory contains lightweight native desktop hosts that bundle and run
the AList server locally.

- `macos`: Swift/AppKit menu bar application with an embedded WebKit window.
- `windows`: .NET 8/WPF tray application that opens AList in the default browser.

Both applications bind AList to `127.0.0.1` by default, keep runtime data in
the current user's application-data directory, and expose an explicit setting
for LAN access.

## Build

```bash
./scripts/build-alist-desktop-macos.sh
```

```powershell
.\scripts\build-alist-desktop-windows.ps1 -Runtime win-x64
```

The `desktop` GitHub Actions workflow builds both packages for pull requests,
relevant pushes to `main`, manual runs, and releases. Release runs also attach
the generated archives to the GitHub release.
