import { createReadStream, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, resolve, sep } from "node:path";

const types = new Map([
	[".html", "text/html; charset=utf-8"],
	[".js", "text/javascript; charset=utf-8"],
	[".mjs", "text/javascript; charset=utf-8"],
	[".wasm", "application/wasm"],
]);

export function fixtureServer(directory) {
	const root = resolve(directory);
	return createServer((request, response) => {
		const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
		const file = resolve(root, pathname.replace(/^\/+/, "") || "browser.html");
		if (file !== root && !file.startsWith(root + sep)) {
			response.writeHead(403).end();
			return;
		}
		try {
			if (!statSync(file).isFile()) {
				throw new Error("not a file");
			}
			response.writeHead(200, {
				"Content-Type": types.get(extname(file)) || "application/octet-stream",
				"Cross-Origin-Embedder-Policy": "require-corp",
				"Cross-Origin-Opener-Policy": "same-origin",
				"Cross-Origin-Resource-Policy": "same-origin",
			});
			createReadStream(file).pipe(response);
		} catch {
			response.writeHead(404).end();
		}
	});
}
