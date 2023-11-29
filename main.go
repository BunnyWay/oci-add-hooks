package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

const (
	// Size of the buffer for catching os.Signal sent to this process
	signalBufferSize = 32
	exitCodeFailure  = 1
)

var (
	errUnableToFindRuntime = errors.New("unable to find runtime")

	commit string
)

func main() {
	// We are doing manual flag parsing b/c the default flag package
	// doesn't have the ability to parse only some flags and ignore unknown
	// ones. Just requiring positional arguments for simplicity.
	// We are expecting command line like one of the following:
	// self --version
	// self --hook-config-path /path/to/hookcfg --runtime-path /path/to/kata, ... runtime flags
	// If we don't match one of these these, we can exit
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("commit:", commit)
		os.Exit(0)
	} else if len(os.Args) < 6 || (os.Args[1] != "--hook-config-path" && os.Args[3] != "--runtime-path") {
		os.Exit(exitCodeFailure)
	}
	// If are args are present, grab the values
	hookConfigPath := os.Args[2]
	kataPath := os.Args[4]
	passthroughArgs := os.Args[5:]
	os.Exit(run(hookConfigPath, kataPath, passthroughArgs))
}

func run(hookConfigPath, kataPath string, kataArgs []string) int {
	// If required args aren't present, bail
	if hookConfigPath == "" || kataPath == "" {
		return exitCodeFailure
	}

	// If a hookConfigPath passed, process the bundle and pass modified
	// spec to kata
	return processBundle(hookConfigPath, kataPath, kataArgs)
}

func processBundle(hookPath, kataPath string, kataArgs []string) int {
	// find the bundle json location
	for i, val := range kataArgs {
		if val == "--bundle" && i != len(kataArgs)-1 {
			// get the bundle Path
			bundlePath := kataArgs[i+1]
			bundlePath = filepath.Join(bundlePath, "config.json")
			// Add the hooks from hookPath to our bundle/config.json
			merged, err := addHooks(bundlePath, hookPath)
			if err != nil {
				return exitCodeFailure
			}
			err = merged.writeFile(bundlePath)
			if err != nil {
				return exitCodeFailure
			}
			break
		}
	}
	// launch kata
	path, err := verifyRuntimePath(kataPath)
	if err != nil {
		return exitCodeFailure
	}
	return launchKata(path, kataArgs)
}

func verifyRuntimePath(userDefinedKataPath string) (string, error) {
	info, err := os.Stat(userDefinedKataPath)
	if err == nil && !info.Mode().IsDir() && info.Mode().IsRegular() {
		return userDefinedKataPath, nil
	}
	return "", errUnableToFindRuntime
}

// Launch kata with the provided args
func launchKata(kataPath string, kataArgs []string) int {
	cmd := prepareCommand(kataPath, kataArgs)
	proc := make(chan os.Signal, signalBufferSize)
	// Handle signals before we start command to make sure we don't
	// miss any related to cmd.
	signal.Notify(proc)
	err := cmd.Start()
	if err != nil {
		return exitCodeFailure
	}
	// Forward signals after we start command
	go func() {
		for sig := range proc {
			cmd.Process.Signal(sig)
		}
	}()

	err = cmd.Wait()

	return processKataError(err)
}

func processKataError(err error) int {
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			// We had a nonzero exitCode
			if code, ok := exit.Sys().(syscall.WaitStatus); ok {
				// and the code is retrievable
				// so we exit with the same code
				return code.ExitStatus()
			}
		}
		// If we can't get the error code, still exit with error
		return exitCodeFailure
	}
	return 0
}

func prepareCommand(kataPath string, args []string) *exec.Cmd {
	cmd := exec.Command(kataPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// Add hooks specified inside hookPath to the bundle specified in args
func addHooks(bundlePath, hookPath string) (*config, error) {
	specHooks, err := readHooks(bundlePath)
	if err != nil {
		return nil, err
	}
	addHooks, err := readHooks(hookPath)
	if err != nil {
		return nil, err
	}
	specHooks.merge(addHooks)
	return specHooks, nil
}
