$ErrorActionPreference = 'Stop'
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

Push-Location $repositoryRoot
try {
    docker-compose stop backend1 | Out-Null
    Start-Sleep -Seconds 12
    1..20 | ForEach-Object {
        $response = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8080/' -TimeoutSec 3
        if ($response.StatusCode -ne 200) { throw "Request failed with HTTP $($response.StatusCode)" }
    }
    $readiness = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8080/readyz' -TimeoutSec 3
    if ($readiness.StatusCode -ne 200) { throw 'Balancer lost readiness while one backend was unavailable.' }
    Write-Output 'Single-backend failure scenario passed.'
}
finally {
    docker-compose start backend1 | Out-Null
    Pop-Location
}
