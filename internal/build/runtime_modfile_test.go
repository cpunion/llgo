package build

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// An application's alternate module file must not replace the compiler's
// explicitly selected runtime while the runtime package graph is loaded.
func TestRuntimePackageLoadUsesItsOwnModule(t *testing.T) {
	for _, mode := range []string{"buildflag-equals", "buildflag-separate", "goflags", "goflags-quoted"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			write := func(dir, name, source string) {
				t.Helper()
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			const path = "example.org/compiler-runtime"
			local, stale := filepath.Join(root, "runtime"), filepath.Join(root, "stale")
			for _, dir := range []string{local, stale} {
				write(dir, "go.mod", "module "+path+"\n\ngo 1.20\n")
				write(dir, "runtime.go", "package runtime\nconst Source = \""+dir+"\"\n")
			}
			app := filepath.Join(root, "app")
			write(app, "go.mod", "module example.org/app\n\ngo 1.20\n")
			modfile := filepath.Join(app, "alternate module.mod")
			write(app, filepath.Base(modfile), "module example.org/app\n\ngo 1.20\nrequire "+path+" v0.0.0\nreplace "+path+" => "+stale+"\n")
			cfg := &packages.Config{
				Dir: app, Mode: packages.NeedName | packages.NeedFiles | packages.NeedModule,
				Env: withEnv(os.Environ(), "GOWORK=off", "GOENV=off", "GOFLAGS=", "GO111MODULE=on", "GOTOOLCHAIN=local"),
			}
			switch mode {
			case "buildflag-equals":
				cfg.BuildFlags = []string{"-p=1", "-modfile=" + modfile}
			case "buildflag-separate":
				cfg.BuildFlags = []string{"-modfile", modfile, "-p=1"}
			case "goflags":
				modfile = filepath.Join(app, "selected.mod")
				write(app, "selected.mod", "module example.org/app\n\ngo 1.20\nrequire "+path+" v0.0.0\nreplace "+path+" => "+stale+"\n")
				cfg.Env = withEnv(cfg.Env, "GOFLAGS=-p=1 -modfile="+modfile)
			case "goflags-quoted":
				cfg.Env = withEnv(cfg.Env, "GOFLAGS=-p=1 '-modfile="+modfile+"'")
			}
			beforeFlags, beforeEnv := append([]string(nil), cfg.BuildFlags...), append([]string(nil), cfg.Env...)
			alt := runtimePackageLoadConfig(cfg, local, runtime.Version())
			loaded, err := packages.Load(&alt, path)
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 || len(loaded[0].Errors) != 0 || len(loaded[0].GoFiles) != 1 || loaded[0].GoFiles[0] != filepath.Join(local, "runtime.go") {
				if len(loaded) == 1 {
					t.Fatalf("runtime source=%v, errors=%v; want local %s", loaded[0].GoFiles, loaded[0].Errors, local)
				}
				t.Fatalf("loaded %d runtime packages, want one", len(loaded))
			}
			if !reflect.DeepEqual(cfg.BuildFlags, beforeFlags) || !reflect.DeepEqual(cfg.Env, beforeEnv) {
				t.Fatal("runtime configuration mutated the application's module flags")
			}
		})
	}
}

func TestRuntimeModuleFlagsPreserveOtherOptionsAndErrors(t *testing.T) {
	for _, test := range []struct{ input, want []string }{
		{[]string{"-tags=llgo,custom", "-modfile=app.mod", "-p=2"}, []string{"-tags=llgo,custom", "-p=2"}},
		{[]string{"-modfile", "app.mod", "-mod=readonly"}, []string{"-mod=readonly"}},
		{[]string{"-modfile=first.mod", "-modfile=second.mod", "-gcflags=all=-l"}, []string{"-gcflags=all=-l"}},
		{[]string{"-modfile"}, []string{"-modfile"}},
		{[]string{"-modfile", "-p=2"}, []string{"-modfile", "-p=2"}},
	} {
		if got := withoutRuntimeModFileFlags(test.input); !reflect.DeepEqual(got, test.want) {
			t.Errorf("flags %v => %v; want %v", test.input, got, test.want)
		}
	}
	for _, test := range []struct{ input, want string }{
		{"-p=1 '-modfile=alternate module.mod' '-tags=llgo custom'", "-p=1 '-tags=llgo custom'"},
		{`"-modfile=app.mod" "-gcflags=all=-N -l"`, `"-gcflags=all=-N -l"`},
		{"-modfile=first.mod -modfile=second.mod -mod=readonly", "-mod=readonly"},
		{"-modfile", "-modfile"},
		{"'-modfile=unterminated", "'-modfile=unterminated"},
	} {
		if got := withoutRuntimeModFileEnv(test.input); got != test.want {
			t.Errorf("GOFLAGS %q => %q; want %q", test.input, got, test.want)
		}
	}
	bad := runtimePackageLoadConfig(&packages.Config{Env: []string{"GOFLAGS='unterminated"}}, t.TempDir(), runtime.Version())
	if !strings.Contains(strings.Join(bad.Env, "\n"), "GOFLAGS='unterminated") {
		t.Fatal("invalid GOFLAGS quoting was silently repaired")
	}
}
