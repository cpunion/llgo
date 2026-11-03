/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
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

package build

/*
#include <stdlib.h>

// The Go binding exposes only memory-buffer emission. These stable LLVM C
// declarations also let the compiler stream an object to a file, without
// depending on a second copy of the LLVM development-header configuration.
typedef struct LLVMOpaqueTargetMachine *LLVMTargetMachineRef;
typedef struct LLVMOpaqueModule *LLVMModuleRef;
typedef struct LLVMOpaqueValue *LLVMValueRef;
typedef struct LLVMOpaqueBasicBlock *LLVMBasicBlockRef;
typedef enum { LLVMAssemblyFile, LLVMObjectFile } LLVMCodeGenFileType;
extern int LLVMTargetMachineEmitToFile(LLVMTargetMachineRef, LLVMModuleRef,
    const char *, LLVMCodeGenFileType, char **);
extern void LLVMDisposeMessage(char *);
extern void LLVMSetTargetMachineMachineOutliner(LLVMTargetMachineRef, int);
extern const char *LLVMGetModuleInlineAsm(LLVMModuleRef, size_t *);
extern const char *LLVMGetInlineAsmAsmString(LLVMValueRef, size_t *);
extern LLVMValueRef LLVMGetFirstFunction(LLVMModuleRef);
extern LLVMValueRef LLVMGetNextFunction(LLVMValueRef);
extern LLVMBasicBlockRef LLVMGetFirstBasicBlock(LLVMValueRef);
extern LLVMBasicBlockRef LLVMGetNextBasicBlock(LLVMBasicBlockRef);
extern LLVMValueRef LLVMGetFirstInstruction(LLVMBasicBlockRef);
extern LLVMValueRef LLVMGetNextInstruction(LLVMValueRef);
extern int LLVMGetNumOperands(LLVMValueRef);
extern LLVMValueRef LLVMGetOperand(LLVMValueRef, unsigned);
extern LLVMValueRef LLVMIsAInlineAsm(LLVMValueRef);

static int llgo_static_map_opaque_assembly(LLVMModuleRef module) {
    size_t size = 0;
    LLVMGetModuleInlineAsm(module, &size);
    if (size != 0) return 1;
    for (LLVMValueRef fn = LLVMGetFirstFunction(module); fn; fn = LLVMGetNextFunction(fn)) {
        for (LLVMBasicBlockRef bb = LLVMGetFirstBasicBlock(fn); bb; bb = LLVMGetNextBasicBlock(bb)) {
            for (LLVMValueRef inst = LLVMGetFirstInstruction(bb); inst; inst = LLVMGetNextInstruction(inst)) {
                int count = LLVMGetNumOperands(inst);
                for (int i = 0; i < count; i++) {
                    LLVMValueRef operand = LLVMGetOperand(inst, (unsigned)i);
                    LLVMValueRef assembly = operand ? LLVMIsAInlineAsm(operand) : NULL;
                    if (assembly) {
                        LLVMGetInlineAsmAsmString(assembly, &size);
                        if (size != 0) return 2;
                    }
                }
            }
        }
    }
    return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// emitNativeObjectFile avoids both the full native output buffer and the
// C.GoBytes copy of that buffer while LLVM's codegen working set is resident.
func emitNativeObjectFile(machine llvm.TargetMachine, mod llvm.Module, name string) error {
	filename := C.CString(name)
	defer C.free(unsafe.Pointer(filename))
	var message *C.char
	failed := C.LLVMTargetMachineEmitToFile(
		C.LLVMTargetMachineRef(unsafe.Pointer(machine.C)),
		C.LLVMModuleRef(unsafe.Pointer(mod.C)),
		filename, C.LLVMObjectFile, &message,
	)
	if message != nil {
		defer C.LLVMDisposeMessage(message)
	}
	if failed != 0 {
		detail := C.GoString(message)
		if detail == "" {
			detail = "LLVM object emission failed"
		}
		return errors.New(detail)
	}
	return nil
}

// Keep the policy on this TargetMachine; other packages, targets and callers
// retain their own defaults.
func setNativeMachineOutliner(machine llvm.TargetMachine, enabled bool) {
	var flag C.int
	if enabled {
		flag = 1
	}
	C.LLVMSetTargetMachineMachineOutliner(C.LLVMTargetMachineRef(unsafe.Pointer(machine.C)), flag)
}

// Empty compiler identity barriers use only their explicit SSA operands.
// Nonempty assembly and linker metadata need an independent raw-input proof.
func staticMapGoModuleClosed(mod llvm.Module) bool {
	if mod.IsNil() || len(mod.NamedMetadataOperands("llvm.linker.options")) != 0 ||
		len(mod.NamedMetadataOperands("llvm.dependent-libraries")) != 0 {
		return false
	}
	switch C.llgo_static_map_opaque_assembly(C.LLVMModuleRef(unsafe.Pointer(mod.C))) {
	case 0:
		return true
	case 1:
		return false
	}
	// PC-line records contain only a compiler-local label and a numeric ID.
	// Check both their injected content receipt and the complete fixed grammar;
	// a data-anchor marker on arbitrary assembly does not prove raw absence.
	for fn := mod.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for inst := bb.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
				for i := 0; i < inst.OperandsCount(); i++ {
					assembly := inst.Operand(i)
					if assembly.IsAInlineAsm().IsNil() || assembly.InlineAsmString() == "" {
						continue
					}
					if !staticMapCompilerPCLineAnchor(mod.Context(), inst, assembly) {
						return false
					}
				}
			}
		}
	}
	return true
}

func staticMapCompilerPCLineAnchor(ctx llvm.Context, call, assembly llvm.Value) bool {
	if !llssa.IsCoroAtomicCompilerDataAnchor(ctx, call) || call.OperandsCount() != 1 ||
		assembly.InlineAsmConstraintString() != "" || !assembly.InlineAsmHasSideEffects() ||
		assembly.InlineAsmNeedsAlignedStack() || assembly.InlineAsmCanThrow() ||
		assembly.InlineAsmDialect() != llvm.InlineAsmDialectATT {
		return false
	}
	body := assembly.InlineAsmString()
	const prefix = "L__llgo_pcsite_"
	if !strings.HasPrefix(body, prefix) || len(body) < len(prefix)+16 {
		return false
	}
	id := body[len(prefix) : len(prefix)+16]
	for _, char := range id {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	label := prefix + id + "_${:uid}"
	want := fmt.Sprintf("%s:\n.pushsection __DATA,__llgo_pcl,regular,live_support\n.p2align 3\nl_llgo_pcline_rec_${:uid}:\n.quad %s\n.quad 0x%s\n.popsection", label, label, id)
	return body == want
}
