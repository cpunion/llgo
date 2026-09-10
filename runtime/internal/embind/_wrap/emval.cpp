#include <string>
#include <stdint.h>
#include <emscripten.h>
#include <emscripten/val.h>
#include <emscripten/bind.h>
#include <emscripten/version.h>

using namespace emscripten;
using namespace emscripten::internal;

// Emscripten 4.0.11 replaced the private method-caller entry points with the
// invoker API. Keep the old path so existing SDKs remain usable while current
// SDKs use the supported interface.
#ifndef __EMSCRIPTEN_MAJOR__
#define __EMSCRIPTEN_MAJOR__ __EMSCRIPTEN_major__
#define __EMSCRIPTEN_MINOR__ __EMSCRIPTEN_minor__
#define __EMSCRIPTEN_TINY__ __EMSCRIPTEN_tiny__
#endif

#define LLGO_EMVAL_INVOKER_API \
    (__EMSCRIPTEN_MAJOR__ > 4 || \
     (__EMSCRIPTEN_MAJOR__ == 4 && (__EMSCRIPTEN_MINOR__ > 0 || __EMSCRIPTEN_TINY__ >= 11)))

template<typename T>
TYPEID take_typeid() {
    typename WithPolicies<>::template ArgTypeList<T> targetType;
    return targetType.getTypes()[0];    
}

template<typename T, typename... Policies>
EM_VAL take_value(T&& value, Policies...) {
#if LLGO_EMVAL_INVOKER_API
    return val(std::forward<T>(value)).release_ownership();
#else
    typename WithPolicies<Policies...>::template ArgTypeList<T> valueType;
    WireTypePack<T> argv(std::forward<T>(value));
    return _emval_take_value(valueType.getTypes()[0], argv);
#endif
}

template<typename T, typename ...Policies>
T as_value(EM_VAL val, Policies...) {
#if LLGO_EMVAL_INVOKER_API
    _emval_incref(val);
    return emscripten::val::take_ownership(val).as<T>();
#else
    typedef BindingType<T> BT;
    typename WithPolicies<Policies...>::template ArgTypeList<T> targetType;
    
    EM_DESTRUCTORS destructors = nullptr;
    EM_GENERIC_WIRE_TYPE result = _emval_as(
        val,
        targetType.getTypes()[0],
        &destructors);
    DestructorsRunner dr(destructors);
    return fromGenericWireType<T>(result);
#endif
}

struct GoString {
    char *data;
    int len;   
};

static TYPEID typeid_val = take_typeid<val>();

#if LLGO_EMVAL_INVOKER_API
EM_INVOKER take_invoker(int nargs, EM_INVOKER_KIND kind, const TYPEID *types) {
    static thread_local std::vector<EM_INVOKER> invokers[3];
    std::vector<EM_INVOKER>& byArity = invokers[static_cast<int>(kind)];
    if (byArity.size() <= static_cast<size_t>(nargs)) {
        byArity.resize(nargs + 1, nullptr);
    }
    EM_INVOKER& invoker = byArity[nargs];
    if (invoker == nullptr) {
        invoker = _emval_create_invoker(nargs + 1, types, kind);
    }
    return invoker;
}

EM_VAL take_val_result(EM_GENERIC_WIRE_TYPE result) {
    using WireType = BindingType<val>::WireType;
    WireType wire = GenericWireTypeConverter<WireType>::from(result);
    return BindingType<val>::fromWireType(wire).release_ownership();
}
#endif

extern "C" {

// export from llgo
extern GoString llgo_export_string_from(const char *data, int n);
static EM_VAL llgo_emval_normalize(EM_VAL value) {
    return value == 0 ? EM_VAL(internal::_EMVAL_UNDEFINED) : value;
}

EM_VAL llgo_emval_get_global(const char *name) {
    return _emval_get_global(name);
}

EM_VAL llgo_emval_get_module_property(const char *name) {
    return val::module_property(name).release_ownership();
}

static volatile uint8_t llgo_emval_invoke_pending;
static volatile int32_t llgo_emval_js_call_depth;
static bool llgo_emval_invoke_installed;

struct JSCallScope {
    JSCallScope() { ++llgo_emval_js_call_depth; }
    ~JSCallScope() { --llgo_emval_js_call_depth; }
};

static bool llgo_emval_is_exit_status(const val& value) {
    if (value.isUndefined() || value.isNull()) {
        return false;
    }
    val name = value["name"];
    val status = value["status"];
    return !name.isUndefined() && !name.isNull() &&
        name.as<std::string>() == "ExitStatus" &&
        val::global("Number").call<bool>("isInteger", status);
}

static void llgo_emval_capture_exception(const val& jsErr, int *error) {
    // emscripten_force_exit uses ExitStatus as control flow. In particular,
    // os.Exit can race another Asyncify callback and unwind through an active
    // Go-to-JavaScript call. Let the module runner consume that status instead
    // of turning successful process termination into a syscall/js panic.
    if (llgo_emval_is_exit_status(jsErr)) {
        jsErr.throw_();
    }
    *error = 1;
}

void llgo_go_dispatch_sync(uintptr_t handle);

EMSCRIPTEN_KEEPALIVE
void llgo_dispatch_sync(void) {
    val event = val::module_property("llgoWasmSyncInvoke");
    if (event.isUndefined() || event.isNull()) {
        return;
    }
    EM_VAL handle = event.release_ownership();
    llgo_go_dispatch_sync(reinterpret_cast<uintptr_t>(handle));
}

EM_JS(void, llgo_emval_install_invoke_js, (uint8_t *pending_flag, int32_t *js_call_depth), {
    const pending = [];
    const pendingFlag = Number(pending_flag);
    const jsCallDepthPtr = Number(js_call_depth);
    const dispatchSync = (typeof wasmExports === "object" && wasmExports)
        ? (wasmExports["llgo_dispatch_sync"] || wasmExports["_llgo_dispatch_sync"])
        : (Module["_llgo_dispatch_sync"] || Module["llgo_dispatch_sync"]);
    Module["llgoWasmPendingInvokes"] = pending;
    Module["_llgo_invoke"] = function(event) {
        // A Go-initiated JS call is still on LLGo's Asyncify-managed wasm
        // stack (syscall/js Value.Call/Invoke), so dispatch a nested FuncOf
        // callback synchronously. Host events such as setTimeout still queue.
        const heap32 = typeof HEAP32 !== "undefined" ? HEAP32 : Module["HEAP32"];
        if (jsCallDepthPtr && heap32 && heap32[jsCallDepthPtr >> 2] > 0 && typeof dispatchSync === "function") {
            Module["llgoWasmSyncInvoke"] = event;
            try {
                dispatchSync();
                return event.result;
            } finally {
                delete Module["llgoWasmSyncInvoke"];
            }
        }
        pending.push(event);
        HEAPU8[pendingFlag] = 1;
        const state = Module["llgoWasmHostWait"];
        if (state !== undefined && state.wake !== undefined) {
            const wake = state.wake;
            delete state.wake;
            setTimeout(wake, 0);
        }
    };
});

void llgo_emval_install_invoke(void) {
    // FuncOf unregisters polling when the last callback is released, but the
    // module bridge and its queue remain valid for the lifetime of the Wasm
    // instance. Reinstalling here would discard queued host events and can
    // reset js_call_depth while a callback releases itself.
    if (llgo_emval_invoke_installed) {
        return;
    }
    llgo_emval_invoke_installed = true;
    llgo_emval_invoke_pending = 0;
    llgo_emval_js_call_depth = 0;
    llgo_emval_install_invoke_js(
        const_cast<uint8_t *>(&llgo_emval_invoke_pending),
        const_cast<int32_t *>(&llgo_emval_js_call_depth));
}

bool llgo_emval_has_pending_invoke(void) {
    return llgo_emval_invoke_pending != 0;
}

EM_VAL llgo_emval_take_pending_invoke(void) {
    if (llgo_emval_invoke_pending == 0) {
        return 0;
    }
    val pending = val::module_property("llgoWasmPendingInvokes");
    if (pending.isUndefined() || pending["length"].as<unsigned>() == 0) {
        llgo_emval_invoke_pending = 0;
        return 0;
    }
    val event = pending.call<val>("shift");
    if (pending["length"].as<unsigned>() == 0) {
        llgo_emval_invoke_pending = 0;
    }
    return event.release_ownership();
}

EM_VAL llgo_emval_new_double(double v) {
    return take_value(v);
}

EM_VAL llgo_emval_new_string(const char *str) {
    return _emval_new_u8string(str);
}

EM_VAL llgo_emval_new_object() {
    return _emval_new_object();
}

EM_VAL llgo_emval_new_array() {
    return _emval_new_array();
}

void llgo_emval_incref(EM_VAL value) {
    _emval_incref(llgo_emval_normalize(value));
}

void llgo_emval_decref(EM_VAL value) {
    _emval_decref(llgo_emval_normalize(value));
}

void llgo_emval_set_property(EM_VAL object, EM_VAL key, EM_VAL value) {
    _emval_set_property(llgo_emval_normalize(object), llgo_emval_normalize(key), llgo_emval_normalize(value));
}

EM_VAL llgo_emval_get_property(EM_VAL object, EM_VAL key) {
    return _emval_get_property(llgo_emval_normalize(object), llgo_emval_normalize(key));
}

bool llgo_emval_is_number(EM_VAL object) {
    return _emval_is_number(llgo_emval_normalize(object));
}

bool llgo_emval_is_string(EM_VAL object) {
    return _emval_is_string(llgo_emval_normalize(object));
}

bool llgo_emval_in(EM_VAL item, EM_VAL object) {
    return _emval_in(llgo_emval_normalize(item), llgo_emval_normalize(object));
}

bool llgo_emval_delete(EM_VAL object, EM_VAL property) {
    return _emval_delete(llgo_emval_normalize(object), llgo_emval_normalize(property));
}

EM_VAL llgo_emval_typeof(EM_VAL value) {
    return _emval_typeof(llgo_emval_normalize(value));
}

bool llgo_emval_instanceof(EM_VAL object, EM_VAL constructor) {
    return _emval_instanceof(llgo_emval_normalize(object), llgo_emval_normalize(constructor));
}

double llgo_emval_as_double(EM_VAL v) {
    return as_value<double>(llgo_emval_normalize(v));
}

GoString llgo_emval_as_string(EM_VAL v) {
    std::string value = as_value<std::string>(llgo_emval_normalize(v));
    return llgo_export_string_from(value.c_str(), int(value.size()));
}

bool llgo_emval_equals(EM_VAL first, EM_VAL second) {
    return _emval_equals(llgo_emval_normalize(first), llgo_emval_normalize(second));
}

EM_VAL llgo_emval_method_call(EM_VAL object, const char* name, EM_VAL args[], int nargs, int *error) {
    std::vector<TYPEID> arr;
    arr.resize(nargs+1);
    std::vector<GenericWireType> elements;
    elements.resize(nargs);
    GenericWireType *cursor = elements.data();
    arr[0] = typeid_val;
    for (int i = 0; i < nargs; i++) {
        arr[i+1] = typeid_val;
        EM_VAL arg = llgo_emval_normalize(args[i]);
        _emval_incref(arg);
        writeGenericWireTypes(cursor, arg);
    }
#if LLGO_EMVAL_INVOKER_API
    EM_INVOKER caller = take_invoker(nargs, EM_INVOKER_KIND::METHOD, arr.data());
#else
    EM_METHOD_CALLER caller = _emval_get_method_caller(nargs+1,&arr[0],EM_METHOD_CALLER_KIND::FUNCTION);
#endif
    EM_GENERIC_WIRE_TYPE ret;
    try {
        JSCallScope jsCall;
        EM_DESTRUCTORS destructors = nullptr;
#if LLGO_EMVAL_INVOKER_API
        ret = _emval_invoke(caller, llgo_emval_normalize(object), name, &destructors, elements.data());
        DestructorsRunner dr(destructors);
#else
        ret = _emval_call_method(caller, llgo_emval_normalize(object), name, &destructors, elements.data());
#endif
    } catch(const emscripten::val& jsErr) {
        llgo_emval_capture_exception(jsErr, error);
        return EM_VAL(internal::_EMVAL_UNDEFINED);
    }
#if LLGO_EMVAL_INVOKER_API
    return take_val_result(ret);
#else
    return fromGenericWireType<val>(ret).release_ownership();
#endif
}

/*
kind:
FUNCTION = 0,
CONSTRUCTOR = 1,
*/
EM_VAL llgo_emval_call(EM_VAL fn, EM_VAL args[], int nargs, int kind, int *error) {
   std::vector<TYPEID> arr;
   arr.resize(nargs+1);
   std::vector<GenericWireType> elements;
   elements.resize(nargs);
   GenericWireType *cursor = elements.data();
   arr[0] = typeid_val;
   for (int i = 0; i < nargs; i++) {
       arr[i+1] = typeid_val;
       EM_VAL arg = llgo_emval_normalize(args[i]);
       _emval_incref(arg);
       writeGenericWireTypes(cursor, arg);
   }
#if LLGO_EMVAL_INVOKER_API
   EM_INVOKER_KIND invokerKind = kind == 0
       ? EM_INVOKER_KIND::FUNCTION
       : EM_INVOKER_KIND::CONSTRUCTOR;
   EM_INVOKER caller = take_invoker(nargs, invokerKind, arr.data());
#else
   EM_METHOD_CALLER caller = _emval_get_method_caller(nargs+1,&arr[0],EM_METHOD_CALLER_KIND(kind));
#endif
   EM_GENERIC_WIRE_TYPE ret;
   try {
       JSCallScope jsCall;
       EM_DESTRUCTORS destructors = nullptr;
#if LLGO_EMVAL_INVOKER_API
       ret = _emval_invoke(caller, llgo_emval_normalize(fn), nullptr, &destructors, elements.data());
       DestructorsRunner dr(destructors);
#else
       ret = _emval_call(caller, llgo_emval_normalize(fn), &destructors, elements.data());
#endif
   } catch(const emscripten::val& jsErr) {
       llgo_emval_capture_exception(jsErr, error);
       return EM_VAL(internal::_EMVAL_UNDEFINED);
   }
#if LLGO_EMVAL_INVOKER_API
   return take_val_result(ret);
#else
   return fromGenericWireType<val>(ret).release_ownership();
#endif
}

EM_VAL llgo_emval_memory_view_uint8(size_t length, uint8_t *data) {
    val view{ typed_memory_view<uint8_t>(length,data) };
    return view.release_ownership();
}

void llgo_emval_dump(EM_VAL v) {
    v = llgo_emval_normalize(v);
    _emval_incref(v);
    val console = val::global("console");
    console.call<void>("log", val::take_ownership(v));
}

}
