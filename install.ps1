#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo    = "dolphprefect/pawsift-mcp"
$Binary  = "pawsift"
$InstallDir = Join-Path $env:USERPROFILE ".local\bin"

# Only amd64 is shipped for Windows
$Asset = "$Binary-windows-amd64.exe"
$Url   = "https://github.com/$Repo/releases/latest/download/$Asset"
$Dest  = Join-Path $InstallDir "$Binary.exe"

Write-Host "Installing PawSift for windows/amd64..."
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing
Write-Host "  Binary installed to $Dest"

# Register in Claude Code (~/.claude.json)
$ClaudeConfig = Join-Path $env:USERPROFILE ".claude.json"
if (Test-Path $ClaudeConfig) {
    $json = Get-Content $ClaudeConfig -Raw | ConvertFrom-Json
    if (-not $json.mcpServers) { $json | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) }
    $json.mcpServers | Add-Member -NotePropertyName pawsift -NotePropertyValue ([pscustomobject]@{ command = $Dest }) -Force
    $json | ConvertTo-Json -Depth 10 | Set-Content $ClaudeConfig -Encoding UTF8
    Write-Host "  Registered in Claude Code ($ClaudeConfig)"
}

# Register in Gemini CLI (~/.gemini/settings.json)
$GeminiConfig = Join-Path $env:USERPROFILE ".gemini\settings.json"
if (Test-Path $GeminiConfig) {
    $json = Get-Content $GeminiConfig -Raw | ConvertFrom-Json
    if (-not $json.mcpServers) { $json | Add-Member -NotePropertyName mcpServers -NotePropertyValue ([pscustomobject]@{}) }
    $json.mcpServers | Add-Member -NotePropertyName pawsift -NotePropertyValue ([pscustomobject]@{ command = $Dest }) -Force
    $json | ConvertTo-Json -Depth 10 | Set-Content $GeminiConfig -Encoding UTF8
    Write-Host "  Registered in Gemini CLI ($GeminiConfig)"
}

Write-Host ""
Write-Host "PawSift installed successfully! 🐾"
Write-Host ""

# Warn if install dir is not in PATH
$pathDirs = $env:PATH -split ';'
if ($InstallDir -notin $pathDirs) {
    Write-Host "  Note: $InstallDir is not in your PATH."
    Write-Host "  Add it by running:"
    Write-Host "    [Environment]::SetEnvironmentVariable('PATH', `$env:PATH + ';$InstallDir', 'User')"
}
