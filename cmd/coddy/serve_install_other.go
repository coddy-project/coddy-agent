//go:build !windows

package main

func runServeInstallCommand(args []string) error {
	return runServeInstall(args)
}

func runServeUninstallCommand(args []string) error {
	return runServeUninstall(args)
}

func runServeStatusCommand(args []string) error {
	return runServeStatus(args)
}

func runServeStopCommand(args []string) error {
	return runServeStop(args)
}

func runServeRestartCommand(args []string) error {
	return runServeRestart(args)
}
