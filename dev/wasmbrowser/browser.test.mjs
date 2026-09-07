import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { once } from "node:events";
import { copyFile, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { runBrowser } from "./run.mjs";
import { fixtureServer } from "./server.mjs";

test("real browser observes execution completion and asynchronous failures", async t => {
	assert.ok(process.env.LLGO_BROWSER, "LLGO_BROWSER must name Chrome or Chromium");
	const directory = await mkdtemp(join(tmpdir(), "llgo-browser-test-"));
	await copyFile(new URL("./browser.html", import.meta.url), join(directory, "browser.html"));
	const goRoot = execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
	await copyFile(join(goRoot, "lib", "wasm", "wasm_exec.js"), join(directory, "wasm_exec.js"));
	const server = fixtureServer(directory).listen(0, "127.0.0.1");
	await once(server, "listening");
	try {
		for (const [name, body, passed, message] of [
			["go-wasm-host", 'if (!globalThis.fs?.constants || !globalThis.process || globalThis.path.resolve("a", "b") !== "a/b" || typeof globalThis.Go !== "function") throw Error("missing Go wasm host contract"); options.print("wasm timers ok")', true, "wasm timers ok"],
			["async-success", 'setTimeout(() => options.printErr("wasm timers ok"), 25)', true, "wasm timers ok"],
			["async-zero-exit", 'setTimeout(() => { options.print("wasm timers ok"); throw Object.assign(Error("normal exit"), {name: "ExitStatus", status: 0}) }, 25)', true, "wasm timers ok"],
			["factory-zero-exit", 'options.print("wasm timers ok"); throw Object.assign(Error("normal exit"), {name: "ExitStatus", status: 0})', true, "wasm timers ok"],
			["no-marker", "", false, "timed out waiting"],
			["delayed-error", 'setTimeout(() => { throw Error("delayed failure") }, 25)', false, "delayed failure"],
			["marker-then-error", 'options.print("wasm timers ok"); setTimeout(() => { throw Error("failure after marker") }, 25)', false, "failure after marker"],
			["abort", 'setTimeout(() => options.onAbort("abort fixture"), 25)', false, "abort fixture"],
			["nonzero-exit", 'setTimeout(() => options.onExit(2), 25)', false, "status 2"],
			["fatal-then-normal-exit", 'options.print("wasm timers ok"); options.onExit(2); throw Object.assign(Error("normal unwind"), {name: "ExitStatus", status: 0})', false, "status 2"],
			["rejection", 'setTimeout(() => Promise.reject(Error("rejected fixture")), 25)', false, "rejected fixture"],
		]) {
			await t.test(name, async () => {
				await writeFile(join(directory, `${name}.mjs`), `export default async options => { ${body}; return {}; };`);
				const url = `http://127.0.0.1:${server.address().port}/browser.html?module=${name}.mjs&deadline=1000`;
				const result = await runBrowser(process.env.LLGO_BROWSER, url, { wallTime: 15000, virtualTime: 1500 });
				assert.equal(result.passed, passed, result.html + result.log);
				assert.match(result.html, passed ? /<body\b[^>]*data-result="pass"/ : /<body\b[^>]*data-result="fail"/);
				assert.ok(result.html.split("<script")[0].includes(message), result.html + result.log);
			});
		}
		await t.test("blocked renderer has a wall-clock limit", async () => {
			let requestedHang = false;
			server.on("request", request => { if (request.url === "/hang.mjs") requestedHang = true; });
			await writeFile(join(directory, "hang.mjs"), "export default () => { while (true) {} };");
			const url = `http://127.0.0.1:${server.address().port}/browser.html?module=hang.mjs`;
			const result = await runBrowser(process.env.LLGO_BROWSER, url, { wallTime: 10000 });
			assert.ok(requestedHang, "browser never requested the blocking fixture");
			assert.equal(result.passed, false);
			assert.equal(result.timedOut, true, result.html + result.log);
		});
	} finally {
		await new Promise(resolve => server.close(resolve));
		await rm(directory, { recursive: true, force: true });
	}
});
