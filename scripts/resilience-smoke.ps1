param([string]$Image='nebulakv:local')
$ErrorActionPreference='Stop'
$id=[guid]::NewGuid().ToString('N').Substring(0,10)
$network="nebulakv-check-$id"
$primary="nebulakv-primary-$id"
$replica="nebulakv-replica-$id"
$fixture=Join-Path ([IO.Path]::GetTempPath()) "nebulakv-secret-$id.txt"
$password="test-only-$id"
$created=@()
$networkCreated=$false
function Docker-Checked {
    param([string[]]$Arguments)
    $output=& docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw 'Docker test command failed.' }
    return $output
}
function Redis {
    param([string]$Server,[string[]]$Command,[switch]$NoAuth)
    $args=@('run','--rm','--network',$network)
    if (-not $NoAuth) { $args+=@('-e',"REDISCLI_AUTH=$password") }
    $args+=@('redis:8-alpine','redis-cli','-h',$Server,'-p','6380','--raw')+$Command
    return Docker-Checked -Arguments $args
}
function Expect {
    param([string]$Server,[string[]]$Command,[string]$Expected)
    $actual=Redis -Server $Server -Command $Command
    if ($actual -ne $Expected) { throw "Unexpected reply for $($Command[0]): $actual" }
}
function Eventually {
    param([string]$Server,[string[]]$Command,[string]$Expected)
    for ($attempt=0;$attempt -lt 30;$attempt++) {
        if ((Redis -Server $Server -Command $Command) -eq $Expected) { return }
        Start-Sleep -Milliseconds 100
    }
    throw "Replica did not converge for $($Command[0])."
}
function Info-Number {
    param([string]$Server,[string]$Field)
    $lines=Redis -Server $Server -Command @('INFO')
    $line=$lines | Where-Object { $_.StartsWith("${Field}:") } | Select-Object -First 1
    if (-not $line) { throw "Missing INFO field: $Field" }
    return [long]($line.Substring($Field.Length+1))
}
try {
    [IO.File]::WriteAllText($fixture,$password,[Text.UTF8Encoding]::new($false))
    Docker-Checked -Arguments @('network','create','--internal',$network) | Out-Null
    $networkCreated=$true
    $common=@('--network',$network,'--memory','256m','--memory-swap','256m','--mount',"type=bind,source=$fixture,target=/run/secret,readonly")
    Docker-Checked -Arguments (@('run','-d','--name',$primary)+$common+@($Image,'--host','0.0.0.0','--appendonly','--data','/data','--password-file','/run/secret','--maxmemory','4096','--aof-rewrite-size','0')) | Out-Null
    $created+=$primary
    $reply=Redis -Server $primary -Command @('GET','secret') -NoAuth
    if ($reply -notlike '*NOAUTH*') { throw 'Unauthenticated read was not rejected.' }
    Expect -Server $primary -Command @('SET','keep','value') -Expected 'OK'
    $oversized='x'*4096
    $reply=Redis -Server $primary -Command @('MSET','keep','changed','too-big',$oversized)
    if ($reply -notlike '*OOM*') { throw 'Memory limit did not reject oversized MSET.' }
    Expect -Server $primary -Command @('GET','keep') -Expected 'value'
    Write-Output 'PASS: AUTH gate and atomic OOM rejection'
    $filler='z'*512
    for ($i=0;$i -lt 12;$i++) { Expect -Server $primary -Command @('SET','history',$filler) -Expected 'OK' }
    Expect -Server $primary -Command @('SET','history','final') -Expected 'OK'
    $before=Info-Number -Server $primary -Field 'aof_bytes'
    Expect -Server $primary -Command @('REWRITEAOF') -Expected 'OK'
    $after=Info-Number -Server $primary -Field 'aof_bytes'
    if ($after -ge $before) { throw 'Rewrite did not reduce journal size.' }
    Write-Output "PASS: journal compacted from $before to $after bytes"
    Docker-Checked -Arguments (@('run','-d','--name',$replica)+$common+@($Image,'--host','0.0.0.0','--appendonly','--data','/data','--password-file','/run/secret','--maxmemory','4096','--replicaof',"${primary}:6380",'--primary-password-file','/run/secret','--replica-interval','100ms')) | Out-Null
    $created+=$replica
    Eventually -Server $replica -Command @('GET','history') -Expected 'final'
    $reply=Redis -Server $replica -Command @('SET','forbidden','write')
    if ($reply -notlike '*READONLY*') { throw 'Replica accepted a write.' }
    Expect -Server $primary -Command @('SET','replicated','one') -Expected 'OK'
    Eventually -Server $replica -Command @('GET','replicated') -Expected 'one'
    Expect -Server $primary -Command @('SET','temporary','v','PX','1000') -Expected 'OK'
    Start-Sleep -Seconds 2
    Eventually -Server $replica -Command @('PTTL','temporary') -Expected '-2'
    Write-Output 'PASS: authenticated replication, read-only enforcement and TTL'
    Docker-Checked -Arguments @('kill','--signal','KILL',$primary) | Out-Null
    Expect -Server $replica -Command @('GET','replicated') -Expected 'one'
    Docker-Checked -Arguments @('start',$primary) | Out-Null
    Expect -Server $primary -Command @('GET','history') -Expected 'final'
    Expect -Server $primary -Command @('SET','replicated','two') -Expected 'OK'
    Eventually -Server $replica -Command @('GET','replicated') -Expected 'two'
    Docker-Checked -Arguments @('restart',$replica) | Out-Null
    Eventually -Server $replica -Command @('GET','replicated') -Expected 'two'
    Write-Output 'PASS: primary abrupt restart, automatic resynchronization and replica restart'
    $memory=Docker-Checked -Arguments @('inspect',$primary,'--format','{{.HostConfig.Memory}}')
    if ([long]$memory -ne 268435456) { throw 'Container memory ceiling was not applied.' }
    Write-Output "PASS: Docker process memory ceiling configured at $memory bytes"
    Redis -Server $replica -Command @('INFO')
} finally {
    foreach ($name in $created) { & docker rm -f $name | Out-Null; if ($LASTEXITCODE -ne 0) { Write-Warning "Cleanup failed for $name" } }
    if ($networkCreated) { & docker network rm $network | Out-Null; if ($LASTEXITCODE -ne 0) { Write-Warning "Cleanup failed for $network" } }
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture }
}
