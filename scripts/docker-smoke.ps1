param([string]$Image='nebulakv:local')
$ErrorActionPreference='Stop'
$name='nebulakv-smoke-'+[guid]::NewGuid().ToString('N').Substring(0,10)
function Invoke-DockerChecked {
    param([string[]]$Arguments)
    $result=& docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $($Arguments -join ' ')" }
    return $result
}
function Invoke-Redis {
    param([string[]]$Command)
    return Invoke-DockerChecked -Arguments (@('run','--rm','--network',"container:$name",'redis:8-alpine','redis-cli','-p','6380','--raw')+$Command)
}
function Assert-Redis {
    param([string[]]$Command,[string]$Expected)
    $actual=Invoke-Redis -Command $Command
    if ($actual -ne $Expected) { throw "Expected '$Expected', received '$actual' for $($Command -join ' ')" }
    Write-Output "PASS: $($Command -join ' ') -> $actual"
}
try {
    Invoke-DockerChecked -Arguments @('run','-d','--name',$name,$Image,'--host','0.0.0.0','--appendonly','--data','/data') | Out-Null
    Assert-Redis -Command @('PING') -Expected 'PONG'
    Assert-Redis -Command @('SET','greeting','hello') -Expected 'OK'
    Assert-Redis -Command @('GET','greeting') -Expected 'hello'
    Assert-Redis -Command @('SET','temporary','value','PX','1000') -Expected 'OK'
    Start-Sleep -Seconds 2
    Assert-Redis -Command @('PTTL','temporary') -Expected '-2'
    Assert-Redis -Command @('SET','counter','10') -Expected 'OK'
    Assert-Redis -Command @('INCR','counter') -Expected '11'
    Assert-Redis -Command @('SET','greeting','ignored','NX','GET') -Expected 'hello'
    Invoke-Redis -Command @('INFO')
    Invoke-DockerChecked -Arguments @('restart',$name) | Out-Null
    Assert-Redis -Command @('GET','greeting') -Expected 'hello'
    Assert-Redis -Command @('GET','counter') -Expected '11'
    # Abrupt termination verifies that acknowledged writes do not need shutdown.
    Invoke-DockerChecked -Arguments @('kill','--signal','KILL',$name) | Out-Null
    Invoke-DockerChecked -Arguments @('start',$name) | Out-Null
    Assert-Redis -Command @('GET','greeting') -Expected 'hello'
    Assert-Redis -Command @('GET','counter') -Expected '11'
    Write-Output 'PASS: redis-cli compatibility, graceful restart and abrupt restart'
} finally {
    & docker rm -f $name | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Warning "Could not remove test container $name" }
}
