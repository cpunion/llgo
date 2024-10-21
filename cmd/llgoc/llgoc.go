package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/llgen"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/xtool/clang"
	"github.com/goplus/llgo/xtool/env/llvm"
)

func main() {
	fmt.Fprintf(os.Stderr, "args: %v\n", os.Args)

	customFlags := flag.NewFlagSet("compile", flag.ContinueOnError)
	customFlags.SetOutput(os.Stderr)

	// 定义命令行参数
	output := customFlags.String("o", "", "write output to `file`")
	trimpath := customFlags.String("trimpath", "", "remove `prefix` from recorded source file paths")
	packagePath := customFlags.String("p", "", "set expected package import `path`")
	buildID := customFlags.String("buildid", "", "record `id` as the build id in the export metadata")
	goVersion := customFlags.String("goversion", "", "required `version` of the runtime")
	symabis := customFlags.String("symabis", "", "read symbol ABIs from `file`")
	importcfg := customFlags.String("importcfg", "", "read import configuration from `file`")
	asmhdr := customFlags.String("asmhdr", "", "write assembly header to `file`")

	versionFlag := customFlags.String("V", "no", "print version and exit")
	std := customFlags.Bool("std", false, "compiling standard library")
	complete := customFlags.Bool("complete", false, "compiling complete package (no C or assembly)")
	shared := customFlags.Bool("shared", false, "generate code that can be linked into a shared library")
	nolocalimports := customFlags.Bool("nolocalimports", false, "reject local (relative) imports")
	pack := customFlags.Bool("pack", false, "write to file.a instead of file.o")

	c := customFlags.Int("c", 10, "concurrency during compilation (1 means no concurrency)")

	compilingRuntime := customFlags.Bool("+", false, "compiling runtime")

	err := customFlags.Parse(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			fmt.Fprintf(os.Stderr, "usage: compile [options] file.go...\n")
			customFlags.PrintDefaults()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *versionFlag != "no" {
		fmt.Println("compile version 0.1.0")
		os.Exit(0)
	}

	sourceFiles := customFlags.Args()

	fmt.Println("输出文件:", *output)
	fmt.Println("Trimpath:", *trimpath)
	fmt.Println("包路径:", *packagePath)
	fmt.Println("构建 ID:", *buildID)
	fmt.Println("Go 版本:", *goVersion)
	fmt.Println("Symabis 文件:", *symabis)
	fmt.Println("Importcfg 文件:", *importcfg)
	fmt.Println("汇编头文件:", *asmhdr)
	fmt.Println("标准库:", *std)
	fmt.Println("完整包:", *complete)
	fmt.Println("共享对象:", *shared)
	fmt.Println("禁止本地导入:", *nolocalimports)
	fmt.Println("写入包文件:", *pack)
	fmt.Println("并发编译数量:", *c)
	fmt.Println("编译运行时:", *compilingRuntime)
	fmt.Println("源文件:", strings.Join(sourceFiles, ", "))

	if *output == "" || *packagePath == "" || len(sourceFiles) == 0 {
		fmt.Fprintf(os.Stderr, "usage: compile [options] file.go...\n")
		customFlags.PrintDefaults()
		os.Exit(1)
	}

	fmt.Printf("sourceFiles: %v\n", sourceFiles)

	if !*pack {
		// TODO(lijie): doesn't support non-package file yet
		fmt.Fprintf(os.Stderr, "output file must be a package file\n")
		os.Exit(1)
	}

	llgen.Verbose = false
	llssa.Initialize(llssa.InitAll)
	llssa.SetDebug(0)
	// llssa.SetDebug(llssa.DbgFlagAll)
	cl.SetDebug(0)
	// cl.SetDebug(cl.DbgFlagAll)
	cl.EnableDebugSymbols(false)

	llFile := *output + ".ll"
	llOut := llgen.GenFrom(*asmhdr, *packagePath, sourceFiles...)
	err = os.WriteFile(llFile, []byte(llOut), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing LLVM IR to %s: %v\n", llFile, err)
		os.Exit(1)
	}

	env := llvm.New("")
	os.Setenv("PATH", env.BinDir()+":"+os.Getenv("PATH"))

	objFile := *output + ".o"
	cl := clang.New("clang")
	if err := cl.Exec("-c", "-o", objFile, llFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error compiling LLVM IR %s to %s: %v\n", llFile, objFile, err)
		os.Exit(1)
	}

	ar := clang.New("ar")
	if err := ar.Exec("rcs", *output, objFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating archive %s: %v\n", *output, err)
		os.Exit(1)
	}
}
