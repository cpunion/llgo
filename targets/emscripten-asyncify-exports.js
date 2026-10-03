// Emscripten 6.0.8's MetaDCE does not recognize the chained assignments for
// EXPORT_ALL Asyncify exports. These functions are added after wasm-ld, so
// EXPORTED_FUNCTIONS cannot name them. Simple bindings make the generated
// controls visible to MetaDCE without changing the public Module exports.
function llgoCheckAsyncifyExports() {
	var startUnwind = wasmExports["asyncify_start_unwind"];
	var stopUnwind = wasmExports["asyncify_stop_unwind"];
	var startRewind = wasmExports["asyncify_start_rewind"];
	var stopRewind = wasmExports["asyncify_stop_rewind"];
	if (typeof startUnwind !== "function" || typeof stopUnwind !== "function" ||
		typeof startRewind !== "function" || typeof stopRewind !== "function") {
		throw new Error("LLGo: missing Asyncify control exports");
	}
}
var llgoPreRun = Module["preRun"] || [];
if (typeof llgoPreRun === "function") llgoPreRun = [llgoPreRun];
Module["preRun"] = [llgoCheckAsyncifyExports, ...llgoPreRun];
