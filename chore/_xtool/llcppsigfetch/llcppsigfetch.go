/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goplus/llgo/c"
	"github.com/goplus/llgo/c/cjson"
	"github.com/goplus/llgo/chore/_xtool/llcppsigfetch/parse"
	"github.com/goplus/llgo/chore/_xtool/llcppsymg/config"
)

func main() {
	if len(os.Args) == 1 {
		// run with default config file
		runFromConfig()
		return
	}

	if os.Args[1] == "--extract" {
		runExtract()
	} else if os.Args[1] == "--help" || os.Args[1] == "-h" {
		printUsage()
	} else {
		runFromConfig()
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  llcppsigfetch [<config_file>]")
	fmt.Println("  OR")
	fmt.Println("  llcppsigfetch --extract <file> <temp> [args...]")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  [<config_file>]: Path to the configuration file (use '-' for stdin)")
	fmt.Println("                   If not provided, uses default 'llcppg.cfg'")
	fmt.Println("")
	fmt.Println("  --extract:       Extract information from a single file")
	fmt.Println("    <file>:        Path to the file to process, or file content if -temp=true")
	fmt.Println("    -temp=<bool>:  Optional. Set to 'true' if <file> contains file content,")
	fmt.Println("                   'false' (default) if it's a file path")
	fmt.Println("    -cpp=<bool>:   Optional. Set to 'true' if the language is C++ (default: true)")
	fmt.Println("                   If not present, <file> is a file path")
	fmt.Println("    [args]:        Optional additional arguments")
	fmt.Println("                   Default for C++: -x c++")
	fmt.Println("                   Default for C: -x c")
	fmt.Println("")
	fmt.Println("  --help, -h:      Show this help message")
	fmt.Println("")
	fmt.Println("Note: The two usage modes are mutually exclusive. Use either [<config_file>] OR --extract, not both.")
}

func runFromConfig() {
	cfgFile := "llcppg.cfg"
	if len(os.Args) > 1 {
		cfgFile = os.Args[1]
	}

	var data []byte
	var err error
	if cfgFile == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(cfgFile)
	}
	check(err)

	conf, err := config.GetConf(data)
	check(err)
	defer conf.Delete()

	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to parse config file:", cfgFile)
	}

	files := getHeaderFiles(conf.CFlags, conf.Include)

	context := parse.NewContext(conf.Cplusplus)
	err = context.ProcessFiles(files)
	check(err)

	outputInfo(context)
}

func runExtract() {
	if len(os.Args) < 3 {
		fmt.Println("Error: Insufficient arguments for --extract")
		printUsage()
		os.Exit(1)
	}

	cfg := &parse.Config{
		File:  os.Args[2],
		Args:  []string{},
		IsCpp: true,
		Temp:  false,
	}

	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "-temp="):
			cfg.Temp = parseBoolArg(arg, "temp", false)
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
		case strings.HasPrefix(arg, "-cpp="):
			cfg.IsCpp = parseBoolArg(arg, "cpp", true)
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
		default:
			cfg.Args = append(cfg.Args, arg)
		}
	}

	converter, err := parse.NewConverter(cfg)
	check(err)
	_, err = converter.Convert()
	check(err)
	result := converter.MarshalOutputASTFiles()
	cstr := result.Print()
	c.Printf(cstr)
	cjson.FreeCStr(cstr)
	result.Delete()
	converter.Dispose()
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func getHeaderFiles(cflags string, files []string) []string {
	prefix := cflags
	prefix = strings.TrimPrefix(prefix, "-I")
	var paths []string
	for _, f := range files {
		paths = append(paths, filepath.Join(prefix, f))
	}
	return paths
}

func outputInfo(context *parse.Context) {
	info := context.Output()
	str := info.Print()
	defer cjson.FreeCStr(str)
	defer info.Delete()
	c.Printf(str)
}

func parseBoolArg(arg, name string, defaultValue bool) bool {
	parts := strings.SplitN(arg, "=", 2)
	if len(parts) != 2 {
		fmt.Printf("Warning: Invalid -%s= argument, defaulting to %v\n", name, defaultValue)
		return defaultValue
	}
	value, err := strconv.ParseBool(parts[1])
	if err != nil {
		fmt.Printf("Warning: Invalid -%s= value '%s', defaulting to %v\n", name, parts[1], defaultValue)
		return defaultValue
	}
	return value
}
