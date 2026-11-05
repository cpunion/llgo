//go:build windows

// This is a finite, read-only path observation, not a frame certificate.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var finalPath = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

func observe(name string) (_ map[string]any, resultErr error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var identity syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &identity); err != nil {
		return nil, err
	}
	buffer := make([]uint16, 32768)
	n, _, callErr := finalPath.Call(file.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if n == 0 || n >= uintptr(len(buffer)) {
		return nil, fmt.Errorf("GetFinalPathNameByHandleW returned %d: %v", n, callErr)
	}
	actual := syscall.UTF16ToString(buffer[:n])
	physical := actual
	switch {
	case strings.HasPrefix(actual, `\\?\UNC\`):
		physical = `\\` + strings.TrimPrefix(actual, `\\?\UNC\`)
	case strings.HasPrefix(actual, `\\?\`) && len(actual) >= 7 && actual[5:7] == `:\`:
		physical = actual[4:]
	default:
		return nil, fmt.Errorf("native path has no DOS or UNC route: %q", actual)
	}
	if !filepath.IsAbs(physical) || filepath.Clean(physical) != physical {
		return nil, fmt.Errorf("native route is not canonical: %q", actual)
	}
	reopened, err := os.Stat(physical)
	if err != nil || !os.SameFile(info, reopened) {
		return nil, fmt.Errorf("physical reopen did not preserve identity: %v", err)
	}
	evaluated, evalErr := filepath.EvalSymlinks(name)
	return map[string]any{"input": name, "is_absolute": filepath.IsAbs(name),
		"mode": info.Mode().String(), "bytes": info.Size(), "eval_symlinks": evaluated,
		"eval_error": fmt.Sprint(evalErr), "native_final_path": actual,
		"physical_DOS_path": physical, "physical_equals_input": physical == name,
		"physical_equals_input_casefold": strings.EqualFold(physical, name),
		"reopen_same_file":               true, "volume_serial": identity.VolumeSerialNumber,
		"file_index_high": identity.FileIndexHigh, "file_index_low": identity.FileIndexLow}, nil
}

func main() {
	// Exact current LLGo bindAMD64Frame construction, before Core capture.
	root, err := filepath.EvalSymlinks(runtime.GOROOT())
	if err != nil {
		panic(err)
	}
	driver := filepath.Join(root, "bin", "go.exe")
	result := map[string]any{"kind": "ACTUAL_WINDOWS_SDK_DRIVER_PATH_OBSERVATION_ONLY",
		"Go": runtime.Version(), "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH,
		"runtime_GOROOT": runtime.GOROOT(), "environment_GOROOT": os.Getenv("GOROOT"),
		"GODEBUG": os.Getenv("GODEBUG"), "factory_root": root, "factory_driver": driver}
	for role, name := range map[string]string{"SDK_root": root, "SDK_driver": driver} {
		row, err := observe(name)
		if err != nil {
			panic(err)
		}
		result[role] = row
	}
	looked, lookupErr := exec.LookPath(driver)
	result["exec_LookPath"], result["exec_LookPath_error"] = looked, fmt.Sprint(lookupErr)
	command := exec.Command(driver, "env", "-json", "GOROOT", "GOTOOLDIR", "GOVERSION")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOROOT=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOROOT="+root)
	output, err := command.CombinedOutput()
	result["actual_Go_env_output"], result["actual_Go_env_error"] = string(output), fmt.Sprint(err)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
	if err != nil {
		os.Exit(1)
	}
}
