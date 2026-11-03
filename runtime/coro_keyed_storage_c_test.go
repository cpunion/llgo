//go:build !llgo && (darwin || linux)

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCoroKeyedNativeMappingFailureAndUnpublishedRelease(t *testing.T) {
	compiler := os.Getenv("CC")
	if compiler == "" {
		compiler = "cc"
	}
	cc, err := exec.LookPath(compiler)
	if err != nil {
		t.Skipf("C compiler unavailable: %v", err)
	}
	source, err := filepath.Abs("internal/runtime/_wrap/coro_keyed_storage.c")
	if err != nil {
		t.Fatal(err)
	}
	program := `
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#include <sys/mman.h>
#include <sys/types.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <errno.h>

static bool fail_map, fail_unmap, bad_arguments;
static unsigned maps, releases, map_calls, unmap_calls;
static void *test_map(void *,size_t,int,int,int,off_t);
static int test_unmap(void *,size_t);
#define mmap test_map
#define munmap test_unmap
#include ` + strconv.Quote(source) + `
#undef mmap
#undef munmap

static void *test_map(void *hint,size_t bytes,int protection,int flags,int fd,off_t offset) {
 ++map_calls;
 if(hint!=NULL || protection!=(PROT_READ|PROT_WRITE) || flags!=(MAP_PRIVATE|MAP_ANONYMOUS) || fd!=-1 || offset!=0) bad_arguments=true;
 if(fail_map){errno=ENOMEM;return MAP_FAILED;}
 void *p=mmap(hint,bytes,protection,flags,fd,offset);
 if(p!=MAP_FAILED)++maps;
 return p;
}
static int test_unmap(void *p,size_t bytes) {
 ++unmap_calls;
 if(fail_unmap){errno=EINVAL;return -1;}
 int result=munmap(p,bytes);
 if(result==0)++releases;
 return result;
}
int main(void) {
 const uintptr_t bytes=2048*40;
 if(__llgo_coro_keyed_slots_map_v2(0)!=NULL || __llgo_coro_keyed_slots_map_v2(128*1024+1)!=NULL || map_calls!=0) return 1;
 if(__llgo_coro_keyed_slots_unmap_v2(NULL,bytes) || __llgo_coro_keyed_slots_unmap_v2((void*)1,0) || __llgo_coro_keyed_slots_unmap_v2((void*)1,128*1024+1) || unmap_calls!=0) return 2;
 fail_map=true;
 if(__llgo_coro_keyed_slots_map_v2(bytes)!=NULL || maps!=0) return 3;
 fail_map=false;
 unsigned char *winner=__llgo_coro_keyed_slots_map_v2(bytes);
 unsigned char *loser=__llgo_coro_keyed_slots_map_v2(bytes);
 if(winner==NULL || loser==NULL || winner==loser || winner[0]!=0 || winner[bytes-1]!=0 || loser[0]!=0 || loser[bytes-1]!=0) return 4;
 winner[0]=17;winner[bytes-1]=29;loser[0]=53;
 fail_unmap=true;
 if(__llgo_coro_keyed_slots_unmap_v2(loser,bytes) || loser[0]!=53 || releases!=0) return 5;
 fail_unmap=false;
 if(!__llgo_coro_keyed_slots_unmap_v2(loser,bytes) || winner[0]!=17 || winner[bytes-1]!=29 || releases!=1) return 6;
 if(!__llgo_coro_keyed_slots_unmap_v2(winner,bytes) || maps!=2 || releases!=2 || bad_arguments) return 7;
 return 0;
}
`
	dir := t.TempDir()
	input := filepath.Join(dir, "mapping.c")
	if err := os.WriteFile(input, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	for _, optimization := range []string{"Os", "O2"} {
		t.Run(optimization, func(t *testing.T) {
			image := filepath.Join(dir, "mapping-"+optimization)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, cc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-"+optimization, input, "-o", image).CombinedOutput(); err != nil {
				t.Fatalf("compile actual native mapping implementation: %v\n%s", err, out)
			}
			if out, err := exec.CommandContext(ctx, image).CombinedOutput(); err != nil {
				t.Fatalf("native mapping failure/release ownership: %v\n%s", err, out)
			}
		})
	}
}
