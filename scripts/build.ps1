# Windows equivalent of scripts/build.sh: builds web + server into dist\ferry.exe
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
$version = if ($args[0]) { $args[0] } else { (git describe --tags --always 2>$null) }
if (-not $version) { $version = "dev" }
Push-Location web; npm ci --no-audit --no-fund; npm run build; Pop-Location
Get-ChildItem backend/internal/server/webui -Exclude .gitkeep | Remove-Item -Recurse -Force
Copy-Item web/dist/* backend/internal/server/webui -Recurse -Force
New-Item -ItemType Directory -Force dist | Out-Null
Push-Location backend; $env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=$version" -o ../dist/ferry.exe ./cmd/ferry
Pop-Location
if (-not (Test-Path dist/ferry.env)) { & dist/ferry.exe config example | Out-File -Encoding ascii dist/ferry.env }
Write-Host "built dist\ferry.exe ($version)"
