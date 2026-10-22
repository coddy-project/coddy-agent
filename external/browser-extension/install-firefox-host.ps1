param([Parameter(Mandatory=$true)][string]$Executable)
$ErrorActionPreference = 'Stop'
$binaryPath = (Resolve-Path -LiteralPath $Executable).Path
$installDir = Join-Path $env:LOCALAPPDATA 'Coddy\browser-host'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$installedPath = Join-Path $installDir 'coddy-browser-host.exe'
Copy-Item -LiteralPath $binaryPath -Destination $installedPath -Force
$manifestPath = Join-Path $installDir 'dev.coddy.browser.json'
$manifest = @{ name='dev.coddy.browser'; description='Coddy native input for Firefox'; path=$installedPath; type='stdio'; allowed_extensions=@('browser@coddy.dev') }
$json = $manifest | ConvertTo-Json
[IO.File]::WriteAllText($manifestPath, $json, [Text.UTF8Encoding]::new($false))
$registryPath = 'HKCU:\Software\Mozilla\NativeMessagingHosts\dev.coddy.browser'
New-Item -Path $registryPath -Force | Out-Null
Set-Item -LiteralPath $registryPath -Value $manifestPath
Write-Output 'Installed for the current user. Enable native Firefox input in the extension settings.'
