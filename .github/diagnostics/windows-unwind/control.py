import ctypes
import json
import os
from pathlib import Path
import sys
import traceback

import lldb

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "cmd" / "internal" / "lldb"))
import llgo_plugin


def host_address_bits():
    class SystemInfo(ctypes.Structure):
        _fields_ = [
            ("processor", ctypes.c_ulong), ("page_size", ctypes.c_ulong),
            ("minimum", ctypes.c_void_p), ("maximum", ctypes.c_void_p),
            ("cpu_mask", ctypes.c_size_t), ("cpu_count", ctypes.c_ulong),
            ("cpu_type", ctypes.c_ulong), ("granularity", ctypes.c_ulong),
            ("cpu_level", ctypes.c_ushort), ("cpu_revision", ctypes.c_ushort),
        ]

    info = SystemInfo()
    get_info = ctypes.WinDLL("kernel32", use_last_error=True).GetSystemInfo
    get_info.argtypes = [ctypes.POINTER(SystemInfo)]
    get_info.restype = None
    get_info(ctypes.byref(info))
    assert info.maximum and info.maximum >= info.minimum
    return info.maximum.bit_length(), info.maximum


def capture(executable, bits=None, production=False):
    debugger = lldb.SBDebugger.Create()
    debugger.SetAsync(False)
    debugger.HandleCommand("settings set symbols.enable-external-lookup false")
    target = debugger.CreateTarget(executable)
    breakpoint = target.BreakpointCreateByName("control_breakpoint")
    assert breakpoint.GetNumLocations() == 1
    process = None
    original_mask = None
    try:
        process = target.LaunchSimple(None, None, os.getcwd())
        assert process.IsValid() and process.GetState() == lldb.eStateStopped
        assert breakpoint.GetHitCount() == 1
        original_mask = process.GetAddressMask(lldb.eAddressMaskTypeCode)
        if bits is not None:
            process.SetAddressableBits(lldb.eAddressMaskTypeCode, bits)
        if production:
            llgo_plugin._configure_windows_arm64_code_addresses(target, process)
            assert process.GetAddressMask(lldb.eAddressMaskTypeCode) != lldb.LLDB_INVALID_ADDRESS_MASK
        workers = target.FindFirstGlobalVariable("worker_ids")
        assert workers.IsValid() and workers.GetNumChildren() == 2
        worker_ids = [workers.GetChildAtIndex(i).GetValueAsUnsigned() for i in range(2)]
        result = {
            "triple": target.GetTriple(), "original_mask": hex(original_mask),
            "code_mask": hex(process.GetAddressMask(lldb.eAddressMaskTypeCode)),
            "worker_ids": worker_ids, "threads": [],
            "platform_host": target.GetPlatform().IsHost(),
            "process_plugin": process.GetPluginName(),
            "production_host_bits": llgo_plugin._windows_arm64_user_address_bits(),
        }
        for thread in process:
            frame = thread.GetFrameAtIndex(0)
            registers = {name: frame.FindRegister(name).GetValue() for name in ("pc", "sp", "fp", "lr")}
            chain, fp = [], frame.GetFP()
            visited = set()
            for _ in range(8):
                if not fp or fp in visited:
                    break
                visited.add(fp)
                error = lldb.SBError()
                data = process.ReadMemory(fp, 16, error)
                if not error.Success() or len(data) != 16:
                    chain.append({"fp": hex(fp), "error": str(error)})
                    break
                parent = int.from_bytes(data[:8], "little")
                saved_lr = int.from_bytes(data[8:], "little")
                chain.append({"fp": hex(fp), "parent": hex(parent), "saved_lr": hex(saved_lr)})
                fp = parent
            frames = []
            for frame_index in range(min(thread.GetNumFrames(), 64)):
                current = thread.GetFrameAtIndex(frame_index)
                frames.append({
                    "pc": hex(current.GetPC()),
                    "name": current.GetFunctionName() or current.GetSymbol().GetName(),
                    "module": current.GetModule().GetFileSpec().GetFilename(),
                })
            result["threads"].append({
                "tid": thread.GetThreadID(), "registers": registers,
                "fp_chain": chain, "frames": frames,
            })
        selected = process.GetSelectedThread()
        assert "control_breakpoint" in (selected.GetFrameAtIndex(0).GetFunctionName() or "")
        for worker_id in worker_ids:
            thread = next(item for item in result["threads"] if item["tid"] == worker_id)
            assert thread["frames"]
            thread["worker_frame_visible"] = any(
                "worker_wait" in (frame["name"] or "") or "worker_entry" in (frame["name"] or "")
                for frame in thread["frames"])
        if production:
            assert all(item["worker_frame_visible"] for item in result["threads"] if item["tid"] in worker_ids)
        process.SetAddressMask(lldb.eAddressMaskTypeCode, original_mask)
        process.Continue()
        assert process.GetState() == lldb.eStateExited and process.GetExitStatus() == 0
        result["exit_status"] = process.GetExitStatus()
        return result
    finally:
        if process and process.IsValid():
            if original_mask is not None:
                process.SetAddressMask(lldb.eAddressMaskTypeCode, original_mask)
            if process.GetState() != lldb.eStateExited:
                process.Kill()
        lldb.SBDebugger.Destroy(debugger)


def run(executable):
    result = {"lldb": lldb.SBDebugger.GetVersionString()}
    status = 0
    try:
        bits, maximum = host_address_bits()
        result.update(host_maximum=hex(maximum), host_address_bits=bits)
        result["baseline"] = capture(executable)
        result["code_mask_control"] = capture(executable, bits)
        result["production_helper"] = capture(executable, production=True)
    except Exception:
        result["error"] = traceback.format_exc()
        status = 1
    with open("windows-unwind.json", "w", encoding="utf-8") as output:
        json.dump(result, output, indent=2)
    print(json.dumps(result, indent=2), flush=True)
    os._exit(status)
