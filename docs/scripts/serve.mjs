// Serves the site on port 4321, or on --port <n>. The port must be free: when it
// is in use the script stops and names the flag. Under code-server, the site is
// served at the /absproxy/<port>/ path taken from VSCODE_PROXY_URI.
//   node scripts/serve.mjs [--port <n>]        build, then preview
//   node scripts/serve.mjs dev [--port <n>]    dev server with live reload
import { spawnSync, spawn } from 'node:child_process';
import { createServer } from 'node:net';

const args = process.argv.slice(2);
const dev = args.includes('dev');
const portArg = args.indexOf('--port');
const DEFAULT_PORT = 4321;
const wanted = portArg >= 0 ? Number(args[portArg + 1]) : DEFAULT_PORT;
if (portArg >= 0 && !(Number.isInteger(wanted) && wanted > 0 && wanted < 65536)) {
	console.error(`--port needs a number from 1 to 65535, got ${args[portArg + 1] ?? 'nothing'}`);
	process.exit(1);
}

// Binding first proves the port is free: the address is written into the build,
// so the server cannot move to another.
const port = await new Promise((resolve) => {
	const s = createServer().listen(wanted, '127.0.0.1', () => {
		const { port } = s.address();
		s.close(() => resolve(port));
	});
	s.on('error', (err) => {
		const reason = err.code === 'EADDRINUSE' ? 'is in use' : `cannot be used (${err.code ?? err.message})`;
		console.error(`port ${wanted} ${reason}. Pick another with: npm run serve -- --port <n>`);
		process.exit(1);
	});
});

const env = { ...process.env };
let url = `http://127.0.0.1:${port}/`;
const proxy = process.env.VSCODE_PROXY_URI;
if (proxy) {
	// /proxy/ strips the path prefix before forwarding; /absproxy/ keeps it, so links match.
	const target = proxy.replace('/proxy/', '/absproxy/').replace('{{port}}', String(port));
	const abs = new URL(target, 'http://localhost/');
	env.SITE_URL = abs.href;
	url = /^[a-z]+:\/\//i.test(target) ? abs.href : abs.pathname; // a relative template names no host
}

if (!dev) {
	const build = spawnSync('npx', ['astro', 'build'], { stdio: 'inherit', env });
	if (build.status !== 0) process.exit(build.status ?? 1);
}
console.log(`\nOpen ${url}\n`);
const server = spawn('npx', ['astro', dev ? 'dev' : 'preview', '--port', String(port)], { stdio: 'inherit', env });
server.on('exit', (code) => process.exit(code ?? 0));
for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.on(sig, () => server.kill(sig));
