$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if (Test-Path tmp/go/bin/go.exe) { $env:PATH = "$PWD\tmp\go\bin;$env:PATH" }
if (Test-Path tmp/w64devkit/bin/gcc.exe) { $env:PATH = "$PWD\tmp\w64devkit\bin;$env:PATH" }
$env:GOPATH = "$PWD\tmp\gopath"
$env:GOCACHE = "$PWD\tmp\gocache"
$env:CGO_ENABLED = '1'
function Run-Go {
    & go @args
    if ($LASTEXITCODE -ne 0) { throw "go failed: $args" }
}
New-Item -ItemType Directory -Force tmp,dist | Out-Null
Run-Go mod download
Run-Go run ./_generate/language mod/data/language/translations target/language/languages_gen.go language
Run-Go run github.com/amazing-generators/gometagen/cmd/gometagen@v0.2.0 generate -source . -format go -out target -force
Run-Go run github.com/amazing-generators/goconfgen/cmd/goconfgen@v0.0.0-20260613145552-0e8a0e84ac12 -schema settings.yml -out target/settings -pkg settings -formats 'json,yaml' -with-presets=false -with-interfaces=false -force
Run-Go run github.com/amazing-generators/godepsgen/cmd/godepsgen@v0.3.0 -source . -pkg target -out target -skip-licenses -force
Run-Go run github.com/amazing-generators/godepsgen/cmd/godepsgen@v0.3.0 -source cli/go.mod -pkg cli -out target/cli/dependencies_gen.go -skip-licenses -force
Run-Go run github.com/amazing-generators/godepsgen/cmd/godepsgen@v0.3.0 -source gui/go.mod -pkg gui -out target/gui/dependencies_gen.go -force
Run-Go test ./mod/... ./cli/...
Run-Go test -tags ci ./gui/...
Run-Go vet ./mod/... ./cli/...
Run-Go vet -tags ci ./gui/...
Run-Go build '-gcflags=all=-N -l' -o dist/dji-rc-debug.exe ./gui
Run-Go build -o dist/xbox-check.exe ./tools/xbox-check
Run-Go build -o dist/dji-rc-cli.exe ./cli

# The official managed package embeds the native x64 client as an uncompressed resource.
Invoke-WebRequest https://api.nuget.org/v3-flatcontainer/nefarius.vigem.client/1.21.256/nefarius.vigem.client.1.21.256.nupkg -OutFile tmp/vigem.zip
Expand-Archive tmp/vigem.zip tmp/vigem -Force
$assembly = [Reflection.Assembly]::LoadFile("$PWD\tmp\vigem\lib\netstandard2.0\Nefarius.ViGEm.Client.dll")
$resource = $assembly.GetManifestResourceStream('costura64.vigemclient.dll')
if ($null -eq $resource) { throw 'Native ViGEm client resource missing' }
$output = [IO.File]::Create("$PWD\dist\ViGEmClient.dll")
try { $resource.CopyTo($output) } finally { $output.Dispose(); $resource.Dispose() }
Copy-Item scripts/Start-Debug.cmd,docs/DEBUG-TESTING.md,docs/CONNECTION-TEST-RESULTS.md,docs/ViGEm-LICENSE.txt dist
if (-not (Test-Path dist/debug-settings.json)) { Copy-Item scripts/debug-settings.json dist }
$revision = & git rev-parse HEAD
@("Source commit: $revision", 'Windows amd64; Go 1.26+; gcflags all=-N -l; symbols retained', 'Native client: Nefarius.ViGEm.Client NuGet 1.21.256 embedded x64 resource') | Set-Content dist/BUILD-INFO.txt
# Package the clean template, never the user's saved settings or debug logs.
$files = @('dji-rc-debug.exe','dji-rc-cli.exe','xbox-check.exe','ViGEmClient.dll','Start-Debug.cmd','DEBUG-TESTING.md','CONNECTION-TEST-RESULTS.md','ViGEm-LICENSE.txt','BUILD-INFO.txt') | ForEach-Object { Join-Path "$PWD\dist" $_ }
Copy-Item scripts/debug-settings.json tmp/debug-settings.json
$files += "$PWD\tmp\debug-settings.json"
Compress-Archive -LiteralPath $files -DestinationPath dist/dji-rc-xbox-debug-win64.zip -Force
Write-Host 'Debug package ready in dist/'
