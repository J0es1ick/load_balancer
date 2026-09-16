param(
    [int]$BalancerPublicPort = 8080,
    [int]$FrontendPort = 3000,
    [int]$EdgeHttpsPort = 8443
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$environmentPath = Join-Path $repositoryRoot '.env'
$secretDirectory = Join-Path $repositoryRoot 'deploy\secrets'
$adminSecretPath = Join-Path $secretDirectory 'admin_token.txt'
$metricsSecretPath = Join-Path $secretDirectory 'metrics_token.txt'
$environmentLines = [System.Collections.Generic.List[string]]::new()
if (Test-Path -LiteralPath $environmentPath) {
    Get-Content -LiteralPath $environmentPath | ForEach-Object { $environmentLines.Add($_) }
}

function New-RandomSecret {
    $bytes = New-Object byte[] 32
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

function Find-EnvironmentKey([string]$Name) {
    for ($index = 0; $index -lt $environmentLines.Count; $index++) {
        if ($environmentLines[$index] -match "^$([regex]::Escape($Name))=(.*)$") {
            return @{ Index = $index; Value = $Matches[1] }
        }
    }
    return $null
}

function Set-EnvironmentValue([string]$Name, [string]$Value, [bool]$ReplaceExisting = $false) {
    $entry = Find-EnvironmentKey $Name
    if ($null -eq $entry) {
        $environmentLines.Add("$Name=$Value")
        return $Value
    }
    if ($ReplaceExisting -or [string]::IsNullOrWhiteSpace($entry.Value)) {
        $environmentLines[$entry.Index] = "$Name=$Value"
        return $Value
    }
    return $entry.Value
}

function Ensure-RandomSecret([string]$Name) {
    $entry = Find-EnvironmentKey $Name
    if ($null -ne $entry -and -not [string]::IsNullOrWhiteSpace($entry.Value)) {
        return $entry.Value
    }
    return Set-EnvironmentValue $Name (New-RandomSecret)
}

$adminToken = Ensure-RandomSecret 'BALANCER_ADMIN_TOKEN'
[void](Ensure-RandomSecret 'BALANCER_VIEWER_TOKEN')
[void](Ensure-RandomSecret 'BALANCER_OPERATOR_TOKEN')
[void](Ensure-RandomSecret 'BALANCER_DISCOVERY_TOKEN')
$metricsToken = Ensure-RandomSecret 'BALANCER_METRICS_TOKEN'
[void](Set-EnvironmentValue 'GRAFANA_ADMIN_USER' 'admin')
[void](Ensure-RandomSecret 'GRAFANA_ADMIN_PASSWORD')
[void](Ensure-RandomSecret 'POSTGRES_PASSWORD')

$effectivePublicPort = Set-EnvironmentValue 'BALANCER_PUBLIC_PORT' "$BalancerPublicPort" $PSBoundParameters.ContainsKey('BalancerPublicPort')
[void](Set-EnvironmentValue 'FRONTEND_PORT' "$FrontendPort" $PSBoundParameters.ContainsKey('FrontendPort'))
[void](Set-EnvironmentValue 'EDGE_HTTPS_PORT' "$EdgeHttpsPort" $PSBoundParameters.ContainsKey('EdgeHttpsPort'))
[void](Set-EnvironmentValue 'VITE_PUBLIC_URL' "http://localhost:$effectivePublicPort/")

$utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllLines($environmentPath, $environmentLines, $utf8WithoutBom)

New-Item -ItemType Directory -Path $secretDirectory -Force | Out-Null
Set-Content -LiteralPath $adminSecretPath -Value $adminToken -Encoding ascii -NoNewline
Set-Content -LiteralPath $metricsSecretPath -Value $metricsToken -Encoding ascii -NoNewline
Write-Output 'Missing local credentials were initialized; existing .env values were preserved.'
