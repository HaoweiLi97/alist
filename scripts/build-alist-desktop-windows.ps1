param(
    [ValidateSet("win-x64")]
    [string]$Runtime = "win-x64",
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Desktop = Join-Path $Root "desktop\windows"
$Build = Join-Path $Desktop "build"
$Work = Join-Path $Build "work"
$Publish = Join-Path $Build $Runtime

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = (git -C $Root describe --tags --always 2>$null)
    if ([string]::IsNullOrWhiteSpace($Version)) { $Version = "dev" }
}
$Version = $Version.TrimStart("v")
$SafeVersion = $Version.Replace("/", "-")
$DotNetVersion = if ($Version -match '^\d+\.\d+\.\d+([-.][0-9A-Za-z.-]+)?$') { $Version } else { "0.0.0" }

Remove-Item $Build -Recurse -Force -ErrorAction SilentlyContinue
New-Item $Work -ItemType Directory -Force | Out-Null
New-Item $Publish -ItemType Directory -Force | Out-Null

$ServerBinary = Join-Path $Work "alist.exe"
if ($env:ALIST_DESKTOP_ALIST_BINARY) {
    Copy-Item $env:ALIST_DESKTOP_ALIST_BINARY $ServerBinary
} else {
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "1"
    if (-not $env:CC) { $env:CC = "gcc" }
    Push-Location $Root
    try {
        go build -tags=jsoniter -trimpath `
            -ldflags="-s -w -X github.com/alist-org/alist/v3/internal/conf.Version=$Version" `
            -o $ServerBinary .
    } finally {
        Pop-Location
    }
}

dotnet publish (Join-Path $Desktop "AListDesktop.Windows.csproj") `
    --configuration Release `
    --runtime $Runtime `
    --self-contained true `
    -p:PublishSingleFile=true `
    -p:DebugType=None `
    -p:DebugSymbols=false `
    -p:Version=$DotNetVersion `
    --output $Publish

$BundledServer = Join-Path $Publish "Assets\bin\alist.exe"
New-Item (Split-Path $BundledServer) -ItemType Directory -Force | Out-Null
Copy-Item $ServerBinary $BundledServer

$Archive = Join-Path $Build "AList-Desktop-Windows-x64-$SafeVersion.zip"
Compress-Archive -Path (Join-Path $Publish "*") -DestinationPath $Archive -CompressionLevel Optimal
Write-Host "Created $Archive"
