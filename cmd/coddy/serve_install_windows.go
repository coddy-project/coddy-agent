//go:build windows

package main

func runServeInstallCommand(args []string) error {
	return runWindowsServiceInstall(args, false)
}

func runServeUninstallCommand(args []string) error {
	return runWindowsServiceInstall(args, true)
}
