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
typedef enum { LLVMAssemblyFile, LLVMObjectFile } LLVMCodeGenFileType;
extern int LLVMTargetMachineEmitToFile(LLVMTargetMachineRef, LLVMModuleRef,
    const char *, LLVMCodeGenFileType, char **);
extern void LLVMDisposeMessage(char *);
extern void LLVMSetTargetMachineMachineOutliner(LLVMTargetMachineRef, int);
*/
import "C"

import (
	"errors"
	"unsafe"

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
