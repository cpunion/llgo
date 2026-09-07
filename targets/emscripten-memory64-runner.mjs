import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";

import "./emscripten-node-polyfills.mjs";
import { runEmscriptenModule } from "./emscripten-exit-status.mjs";

if (process.argv.length < 3) {
	throw new Error("usage: node emscripten-memory64-runner.mjs <module.mjs> [arguments...]");
}

// A memory section whose limits use the memory64 flag. Older Node releases
// expose this behind --experimental-wasm-memory64, while newer releases enable
// it by default and reject the obsolete command-line flag. Probe the engine so
// llgo run works with both families without imposing a Node-version-specific
// emulator command on every user.
const memory64Probe = new Uint8Array([
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x05, 0x03, 0x01, 0x04, 0x01,
]);

if (!WebAssembly.validate(memory64Probe)) {
	if (process.env.LLGO_MEMORY64_NODE_RETRY === "1") {
		throw new Error("this Node release does not support WebAssembly Memory64");
	}
	const child = spawnSync(
		process.execPath,
		["--experimental-wasm-memory64", fileURLToPath(import.meta.url), ...process.argv.slice(2)],
		{
			stdio: "inherit",
			env: { ...process.env, LLGO_MEMORY64_NODE_RETRY: "1" },
		},
	);
	if (child.error) {
		throw child.error;
	}
	process.exit(child.status ?? 1);
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
	// Raw GOOS=js/GOARCH=wasm output intentionally omits Node support. Supply
	// its sibling module through Emscripten's supported host injection point.
	options.instantiateWasm = (imports, receiveInstance) => {
		const module = new WebAssembly.Module(wasmBinary);
		const instance = new WebAssembly.Instance(module, imports);
		receiveInstance(instance, module);
		return instance.exports;
	};
}
await runEmscriptenModule(loaded.default, options);
