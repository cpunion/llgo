package main

import (
	"debug/buildinfo"
	"os"
	"runtime"
	"runtime/debug"
)

func main() {
	running, ok := debug.ReadBuildInfo()
	if !ok || running.GoVersion != runtime.Version() || running.Path == "" {
		panic("missing runtime build information")
	}
	settings := make(map[string]string)
	for _, setting := range running.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{"-compiler": runtime.Compiler, "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH} {
		if settings[key] != want {
			panic("incorrect build setting: " + key)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	fileInfo, err := buildinfo.ReadFile(executable)
	if err != nil {
		panic(err)
	}
	if fileInfo.String() != running.String() {
		panic("file and runtime build information differ")
	}
	f, err := os.Open(executable)
	if err != nil {
		panic(err)
	}
	readerInfo, readErr := buildinfo.Read(f)
	closeErr := f.Close()
	if readErr != nil {
		panic(readErr)
	}
	if closeErr != nil {
		panic(closeErr)
	}
	if readerInfo.String() != running.String() {
		panic("ReaderAt and runtime build information differ")
	}
}
