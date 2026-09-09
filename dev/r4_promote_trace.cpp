// Diagnostic-only driver for the unmodified LLVM 22 promotion algorithm.
// The separately compiled upstream source adds counters, not transformations.
#include "llvm/Analysis/AssumptionCache.h"
#include "llvm/IR/Dominators.h"
#include "llvm/IR/Instructions.h"
#include "llvm/IR/LLVMContext.h"
#include "llvm/IR/Module.h"
#include "llvm/IR/Verifier.h"
#include "llvm/IRReader/IRReader.h"
#include "llvm/Passes/PassBuilder.h"
#include "llvm/Support/CommandLine.h"
#include "llvm/Support/Error.h"
#include "llvm/Support/SourceMgr.h"
#include "llvm/Support/raw_ostream.h"
#include "llvm/Transforms/Utils/PromoteMemToReg.h"

namespace llvm {
void TracePromoteMemToReg(ArrayRef<AllocaInst *>, DominatorTree &,
                         AssumptionCache *);
}

int main(int argc, char **argv) {
  if (argc != 2)
    return 2;
  const char *options[] = {argv[0], "-sroa-skip-mem2reg"};
  llvm::cl::ParseCommandLineOptions(2, options);
  llvm::LLVMContext context;
  llvm::SMDiagnostic error;
  auto module = llvm::parseIRFile(argv[1], error, context);
  if (!module) {
    error.print(argv[0], llvm::errs());
    return 1;
  }
  llvm::PassBuilder pb;
  llvm::LoopAnalysisManager loops;
  llvm::FunctionAnalysisManager functions;
  llvm::CGSCCAnalysisManager sccs;
  llvm::ModuleAnalysisManager modules;
  pb.registerModuleAnalyses(modules);
  pb.registerCGSCCAnalyses(sccs);
  pb.registerFunctionAnalyses(functions);
  pb.registerLoopAnalyses(loops);
  pb.crossRegisterProxies(loops, functions, sccs, modules);
  llvm::ModulePassManager passes;
  if (auto failure = pb.parsePassPipeline(passes, "function(sroa)")) {
    llvm::errs() << llvm::toString(std::move(failure)) << '\n';
    return 1;
  }
  passes.run(*module, modules);
  for (auto &fn : *module) {
    if (fn.isDeclaration())
      continue;
    llvm::SmallVector<llvm::AllocaInst *, 32> allocas;
    for (auto &instruction : fn.getEntryBlock())
      if (auto *alloca = llvm::dyn_cast<llvm::AllocaInst>(&instruction))
        if (llvm::isAllocaPromotable(alloca))
          allocas.push_back(alloca);
    llvm::errs() << "R4 promoting " << fn.getName() << " allocas="
                 << allocas.size() << '\n';
    llvm::DominatorTree dom(fn);
    llvm::AssumptionCache assumptions(fn);
    llvm::TracePromoteMemToReg(allocas, dom, &assumptions);
  }
  return llvm::verifyModule(*module, &llvm::errs()) ? 1 : 0;
}
