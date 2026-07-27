package platform

import "strings"

// PowerShell receives the command as a script rather than as a shell word, so
// nothing here escapes, quotes, or strips the caller's text: it is embedded
// verbatim between a prologue and an epilogue.
//
// The prologue switches the output encoding to UTF-8. Without it PowerShell and
// the native tools it spawns write in the console output code page (866 on a
// Russian Windows install), which reaches us as mojibake and makes error
// messages unreadable to the model. Assigning [Console]::OutputEncoding fails
// when the process has no console attached, so it is guarded; $OutputEncoding is
// set unconditionally because it governs what PowerShell pipes to child
// processes.
const powerShellPrologue = "$ProgressPreference = 'SilentlyContinue'\r\n" +
	"try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch { }\r\n" +
	"$OutputEncoding = New-Object System.Text.UTF8Encoding $false\r\n"

// The epilogue restores exit-code fidelity. powershell.exe -Command collapses
// every failure to 1, so `cmd /c exit 3` would be reported as exit status 1.
// $? is captured first because the following `if` overwrites it.
const powerShellEpilogue = "\r\n$__coddyOK = $?\r\n" +
	"if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }\r\n" +
	"if (-not $__coddyOK) { exit 1 }\r\n" +
	"exit 0\r\n"

// wrapPowerShellCommand returns command as a PowerShell script with the
// encoding prologue and exit-code epilogue around it. The command itself is
// preserved byte for byte, so backticks, quotes, and non-ASCII text keep the
// meaning PowerShell would give them if typed at a prompt.
func wrapPowerShellCommand(command string) string {
	var script strings.Builder
	script.Grow(len(powerShellPrologue) + len(command) + len(powerShellEpilogue))
	script.WriteString(powerShellPrologue)
	script.WriteString(command)
	script.WriteString(powerShellEpilogue)
	return script.String()
}
