import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { fixtureServer } from "./server.mjs";

export async function runBrowser(browser, url, { wallTime = 45000, virtualTime = 25000 } = {}) {
	const profile = await mkdtemp(join(tmpdir(), "llgo-chrome-"));
	let html = "", log = "", timedOut = false, snapshotComplete = false;
	try {
		const child = spawn(browser, [
			"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
			"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
			"--disable-component-update", "--disable-default-apps", "--disable-extensions",
			"--password-store=basic", "--use-mock-keychain",
			`--user-data-dir=${profile}`,
			`--virtual-time-budget=${virtualTime}`, "--dump-dom", url,
		], { detached: process.platform !== "win32", stdio: ["ignore", "pipe", "pipe"] });
		const stop = () => {
			// Kill only this isolated Chrome group, including a stuck renderer.
			try {
				if (process.platform === "win32") child.kill("SIGKILL");
				else process.kill(-child.pid, "SIGKILL");
			} catch (error) {
				if (error.code !== "ESRCH") throw error;
			}
		};
		child.stdout.setEncoding("utf8").on("data", data => {
			html += data;
			// --dump-dom emits its final snapshot after the virtual-time budget.
			// Do not wait for unrelated platform shutdown services after that.
			if (html.endsWith("</html>\n")) {
				snapshotComplete = true;
				stop();
			}
		});
		child.stderr.setEncoding("utf8").on("data", data => { log += data; });
		const timer = setTimeout(() => { timedOut = true; stop(); }, wallTime);
		try { await once(child, "close"); }
		finally { clearTimeout(timer); }
		if (timedOut) log += `\nbrowser exceeded ${wallTime}ms wall-clock limit\n`;
		return { html, log, timedOut, passed: !timedOut && snapshotComplete && /<body\b[^>]*data-result="pass"/.test(html) };
	} finally {
		await rm(profile, { recursive: true, force: true });
	}
}

async function main() {
	const [directory, ...modules] = process.argv.slice(2);
	if (!directory || !modules.length || !process.env.LLGO_BROWSER) {
		throw new Error("usage: LLGO_BROWSER=<chrome> node run.mjs <directory> <module.mjs>...");
	}
	const reports = process.env.LLGO_WASM_BROWSER_REPORT_DIR || join(directory, "reports");
	await mkdir(reports, { recursive: true });
	const server = fixtureServer(directory).listen(0, "127.0.0.1");
	await once(server, "listening");
	try {
		for (const module of modules) {
			const url = `http://127.0.0.1:${server.address().port}/browser.html?module=${encodeURIComponent(module)}`;
			const result = await runBrowser(process.env.LLGO_BROWSER, url);
			await writeFile(join(reports, `${module}.html`), result.html);
			await writeFile(join(reports, `${module}.chrome.log`), result.log);
			console.log(`${result.passed ? "PASS" : "FAIL"} browser ${module}`);
			if (!result.passed) {
				console.error(result.html, result.log);
				process.exitCode = 1;
			}
		}
	} finally {
		await new Promise(resolve => server.close(resolve));
	}
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
