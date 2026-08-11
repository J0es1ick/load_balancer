param(
    [int]$BalancerPublicPort = 8080,
    [int]$FrontendPort = 3000,
    [int]$EdgeHttpsPort = 8443
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$environmentPath = Join-Path $repositoryRoot '.env'
$secretDirectory = Join-Path $repositoryRoot 'deploy\secrets'
$secretPath = Join-Path $secretDirectory 'admin_token.txt'

function New-RandomSecret {
    $bytes = New-Object byte[] 32
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

$adminToken = $null
$grafanaPassword = $null
if (Test-Path -LiteralPath $environmentPath) {
    foreach ($line in Get-Content -LiteralPath $environmentPath) {
        if ($line -match '^BALANCER_ADMIN_TOKEN=(.+)$') { $adminToken = $Matches[1].Trim() }
        if ($line -match '^GRAFANA_ADMIN_PASSWORD=(.+)$') { $grafanaPassword = $Matches[1].Trim() }
    }
}

if (-not $adminToken) { $adminToken = New-RandomSecret }
if (-not $grafanaPassword) { $grafanaPassword = New-RandomSecret }

@(
    "BALANCER_ADMIN_TOKEN=$adminToken"
    'GRAFANA_ADMIN_USER=admin'
    "GRAFANA_ADMIN_PASSWORD=$grafanaPassword"
    'POSTGRES_PASSWORD=local-postgres-not-enabled'
    "BALANCER_PUBLIC_PORT=$BalancerPublicPort"
    "FRONTEND_PORT=$FrontendPort"
    "EDGE_HTTPS_PORT=$EdgeHttpsPort"
    "VITE_PUBLIC_URL=http://localhost:$BalancerPublicPort/"
) | Set-Content -LiteralPath $environmentPath -Encoding ascii

New-Item -ItemType Directory -Path $secretDirectory -Force | Out-Null
Set-Content -LiteralPath $secretPath -Value $adminToken -Encoding ascii -NoNewline
Write-Output "Local credentials initialized in .env and deploy/secrets/."
