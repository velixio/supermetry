$ErrorActionPreference = "Stop"

$installerArgs = @($args)
$version = $env:SUPERMETRY_VERSION
if ([string]::IsNullOrWhiteSpace($version)) {
    $version = "latest"
}

for ($index = 0; $index -lt $installerArgs.Count; $index++) {
    $argument = $installerArgs[$index]
    if ($argument -eq "--version") {
        if ($index + 1 -ge $installerArgs.Count) {
            throw "install.ps1: --version requires a value"
        }
        $version = $installerArgs[$index + 1]
        $index++
    }
    elseif ($argument -like "--version=*") {
        $version = $argument.Substring("--version=".Length)
    }
}

switch ($env:PROCESSOR_ARCHITECTURE.ToUpperInvariant()) {
    "AMD64" { $arch = "amd64" }
    "ARM64" { $arch = "arm64" }
    default { throw "install.ps1: unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if ($version -eq "latest") {
    $response = Invoke-WebRequest `
        -Uri "https://github.com/velixio/supermetry/releases/latest" `
        -Method Head `
        -MaximumRedirection 10 `
        -UseBasicParsing
    if ($response.BaseResponse.ResponseUri) {
        $effectiveUri = $response.BaseResponse.ResponseUri.AbsoluteUri
    }
    else {
        $effectiveUri = $response.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    }
    $version = ($effectiveUri.TrimEnd("/") -split "/")[-1] -replace "^v", ""
    $releaseUrl = "https://github.com/velixio/supermetry/releases/latest/download"
}
else {
    $version = $version -replace "^v", ""
    $releaseUrl = "https://github.com/velixio/supermetry/releases/download/v$version"
}

if ($version -notmatch "^[A-Za-z0-9._+-]+$") {
    throw "install.ps1: invalid release version: $version"
}
if (-not [string]::IsNullOrWhiteSpace($env:SUPERMETRY_RELEASE_URL)) {
    $releaseUrl = $env:SUPERMETRY_RELEASE_URL.TrimEnd("/")
}

$archive = "supermetry_${version}_windows_${arch}.zip"
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("supermetry-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
    $archivePath = Join-Path $tempDir $archive
    $sumsPath = Join-Path $tempDir "SHA256SUMS"
    Invoke-WebRequest -Uri "$releaseUrl/$archive" -OutFile $archivePath -UseBasicParsing
    Invoke-WebRequest -Uri "$releaseUrl/SHA256SUMS" -OutFile $sumsPath -UseBasicParsing

    $escapedArchive = [regex]::Escape($archive)
    $checksumLines = @(Get-Content $sumsPath | Where-Object {
        $_ -match "^([0-9A-Fa-f]{64})\s+\*?$escapedArchive$"
    })
    if ($checksumLines.Count -ne 1) {
        throw "install.ps1: checksum entry for $archive is missing or ambiguous"
    }
    $expectedHash = ($checksumLines[0] -split "\s+")[0]
    $actualHash = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash
    if ($actualHash -ne $expectedHash) {
        throw "install.ps1: checksum mismatch for $archive"
    }

    Expand-Archive -Path $archivePath -DestinationPath $tempDir -Force
    $executable = Join-Path $tempDir "supermetry.exe"
    if (-not (Test-Path -Path $executable -PathType Leaf)) {
        throw "install.ps1: release archive does not contain supermetry.exe"
    }

    & $executable install @installerArgs
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
