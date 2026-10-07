# detect-agents.ps1 - probe which console code-agent CLIs are installed.
# Windows twin of detect-agents.sh: the same table (agents.tsv next to this
# script) and the same tab-separated output:
#   agent <TAB> binary_path <TAB> models_cmd <TAB> run_template
#
# The template is the PowerShell column of agents.tsv: `cmd /d /c --% <the
# POSIX command>`. PowerShell has no '<', and its own | and > re-encode text
# (Windows PowerShell 5.1 reads the system code page and writes UTF-16), so the
# redirection is handed to cmd.exe, which moves the bytes as they are. Koda is
# the exception: it takes the brief as an argument. Works on Windows
# PowerShell 5.1 and PowerShell 7.

$ErrorActionPreference = 'SilentlyContinue'

$table = Join-Path $PSScriptRoot 'agents.tsv'
if (-not (Test-Path -LiteralPath $table)) {
    [Console]::Error.WriteLine("detect-agents.ps1: $table is missing")
    exit 1
}

# Invoke-Probe <file> <args> <seconds> - run a probe bounded in time, like
# timeout(1) in the .sh twin: a CLI that waits for input or a login must not
# stall detection. Returns @{ Ok = exit code 0; Text = stdout and stderr }.
function Invoke-Probe([string]$file, [string[]]$argz, [int]$seconds) {
    try {
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $file
        # Our probe arguments carry no spaces, so a joined string is safe and
        # works on Windows PowerShell 5.1, which has no ArgumentList.
        $psi.Arguments = ($argz -join ' ')
        $psi.UseShellExecute = $false
        $psi.RedirectStandardInput = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $proc = [System.Diagnostics.Process]::Start($psi)
        $proc.StandardInput.Close()
        # Drain both streams asynchronously so a chatty probe cannot fill a
        # pipe buffer and deadlock WaitForExit.
        $out = $proc.StandardOutput.ReadToEndAsync()
        $err = $proc.StandardError.ReadToEndAsync()
        if (-not $proc.WaitForExit($seconds * 1000)) {
            try { $proc.Kill() } catch {}
            return @{ Ok = $false; Text = '' }
        }
        $proc.WaitForExit()
        return @{ Ok = ($proc.ExitCode -eq 0); Text = ($out.Result + $err.Result) }
    } catch {
        return @{ Ok = $false; Text = '' }
    }
}

foreach ($raw in Get-Content -LiteralPath $table -Encoding UTF8) {
    $line = $raw.TrimEnd("`r")
    if ($line.Trim() -eq '' -or $line.StartsWith('#')) { continue }
    $cols = $line.Split("`t")
    if ($cols.Length -ne 6) { continue }
    $agent, $bins, $marker, $models, $posix, $pwsh = $cols
    foreach ($cand in $bins.Split(',')) {
        $cand = $cand.Trim()
        $parts = $cand.Split(' ')
        $fixed = @()
        if ($parts.Length -gt 1) { $fixed = $parts[1..($parts.Length - 1)] }
        # Application only: npm puts a .ps1 shim next to the .cmd one, and a
        # .ps1 cannot be started as a process.
        $cmd = Get-Command $parts[0] -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $cmd) { continue }
        $path = $cmd.Path
        # A name on PATH is not enough (`agent` can be an unrelated
        # executable): --version or --help must succeed and print the marker.
        $verified = $false
        foreach ($flag in @('--version', '--help')) {
            $r = Invoke-Probe $path ($fixed + $flag) 10
            if ($r.Ok -and $r.Text.ToLower().Contains($marker)) { $verified = $true; break }
        }
        if (-not $verified) { continue }
        $modelsCmd = ''
        if ($models -ne '-') {
            $r = Invoke-Probe $path ($fixed + $models.Split(' ')) 20
            if ($r.Ok) { $modelsCmd = "$cand $models" }
        }
        Write-Output ($agent + "`t" + $path + "`t" + $modelsCmd + "`t" + $pwsh.Replace('{bin}', $cand))
        break
    }
}
