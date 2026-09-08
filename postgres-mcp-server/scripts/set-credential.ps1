# 作用：把某个 PostgreSQL target 的密码写入 Windows 凭据管理器
# 运行主机：本机
# 调用方：人工（首次接入或轮换 mcp_ro 密码）
# 大概流程：
#   1) 读取 credential_ref（默认 postgres/<target>）和用户名
#   2) 用 cmdkey 写入 Generic 凭据
#   3) 不回显密码
# 勿放密钥：密码只进本机凭据库，不写仓库、不写 postgres-targets.json

param(
    [Parameter(Mandatory = $true)]
    [string]$Target,
    [string]$User = "mcp_ro",
    [string]$CredentialRef = ""
)

$ErrorActionPreference = "Stop"

if (-not $CredentialRef) {
    $CredentialRef = "postgres/$Target"
}

$secure = Read-Host -Prompt "Password for $CredentialRef ($User)" -AsSecureString
$bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try {
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
} finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
}

cmdkey /generic:$CredentialRef /user:$User /pass:$plain | Out-Null
$plain = $null
Write-Output "stored $CredentialRef for user $User (password not printed)"
