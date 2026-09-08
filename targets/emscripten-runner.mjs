import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

import "./emscripten-node-polyfills.mjs";
import { runEmscriptenModule } from "./emscripten-exit-status.mjs";

if (process.argv.length < 3) {
	throw new Error("usage: node emscripten-runner.mjs <module.mjs> [arguments...]");
}

const moduleURL = pathToFileURL(process.argv[2]);
const loaded = await import(moduleURL);
if (typeof loaded.default !== "function") {
	throw new Error(`${process.argv[2]} does not export an Emscripten module factory`);
}
const wasmURL = new URL(moduleURL);
wasmURL.pathname = wasmURL.pathname.replace(/\.(?:m?js)$/, ".wasm");
let wasmBinary;
try {
	wasmBinary = await readFile(wasmURL);
} catch (error) {
	// SINGLE_FILE modules embed their wasm and need no Node-side loader.
	if (error?.code !== "ENOENT") throw error;
}
const options = {
	arguments: process.argv.slice(3),
	preRun: [module => {
		if (module.ENV != null) {
			Object.assign(module.ENV, process.env);
		}
	}],
};
if (wasmBinary != null) {
	// Supply the sibling module through Emscripten's supported host injection
	// point so both raw GJS and named Emscripten profiles use the same loader.
	options.instantiateWasm = (imports, receiveInstance) => {
		const module = new WebAssembly.Module(wasmBinary);
		const instance = new WebAssembly.Instance(module, imports);
		receiveInstance(instance, module);
		return instance.exports;
	};
}
await runEmscriptenModule(loaded.default, options);
