package heap2stack

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestPostInlinePointerJoinRetainsAlignment(t *testing.T) {
	mod := parseModule(t, `
declare nonnull ptr @"github.com/xgo-dev/llgo/runtime/internal/runtime.AllocZ"(i64) allocsize(0)
declare ptr @malloc(i64) allocsize(0) allockind("alloc,uninitialized")

define ptr @select_payload(i1 %temporary, ptr %source, ptr %value) {
entry:
  br i1 %temporary, label %make, label %join
make:
  %local = tail call ptr @"github.com/xgo-dev/llgo/runtime/internal/runtime.AllocZ"(i64 48) allockind("alloc,zeroed")
  %slot = getelementptr inbounds i8, ptr %local, i64 8
  store ptr %value, ptr %slot, align 8
  br label %join
join:
  %storage = phi ptr [ %local, %make ], [ %source, %entry ]
  %field = getelementptr inbounds i8, ptr %storage, i64 8
  %payload = load ptr, ptr %field, align 8
  ret ptr %payload
}

define ptr @return_storage(i1 %temporary, ptr %source) {
entry:
  br i1 %temporary, label %make, label %join
make:
  %local = call ptr @"github.com/xgo-dev/llgo/runtime/internal/runtime.AllocZ"(i64 48) allockind("alloc,zeroed")
  br label %join
join:
  %storage = phi ptr [ %local, %make ], [ %source, %entry ]
  ret ptr %storage
}

define i64 @foreign_storage(i64 %value) {
  %local = call ptr @malloc(i64 48)
  store i64 %value, ptr %local, align 8
  %result = load i64, ptr %local, align 8
  ret i64 %result
}
`)
	tm := tailTarget(t, mod)
	if err := RunAfterInlining(mod, tm, 8); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	body := mod.NamedFunction("select_payload").String()
	if strings.Contains(body, "AllocZ") || !strings.Contains(body, "alloca i8, i64 48, align 8") {
		t.Fatalf("non-escaping pointer join must use aligned stack storage:\n%s", body)
	}
	for name, callee := range map[string]string{"return_storage": "AllocZ", "foreign_storage": "@malloc"} {
		body = mod.NamedFunction(name).String()
		if !strings.Contains(body, callee) || strings.Contains(body, "alloca ") {
			t.Fatalf("%s must retain heap storage:\n%s", name, body)
		}
	}
}
