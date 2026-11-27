//go:build windows

package main

func runServeInstallCommand(args []string) error {
	return runWindowsServiceInstall(args, false)
}

func runServeUninstallCommand(args []string) error {
	return runWindowsServiceInstall(args, true)
}

func serviceHint() (string, bool) {
	return "use `sc.exe query Coddy`, `sc.exe stop Coddy` or `sc.exe start Coddy` to control the Windows service", false
}
