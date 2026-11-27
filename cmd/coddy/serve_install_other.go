//go:build !windows

package main

func runServeInstallCommand(args []string) error {
	return runServeInstall(args)
}

func runServeUninstallCommand(args []string) error {
	return runServeUninstall(args)
}
