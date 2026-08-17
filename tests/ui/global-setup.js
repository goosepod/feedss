const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const port = 4321;
const pidPath = path.resolve('.feedss-ui-test.pid');
const dbPath = path.resolve('test-results', `feedss-ui-test-${process.pid}.db`);

module.exports = async () => {
	if (process.platform === 'win32') {
		spawnSync('powershell.exe', [
			'-NoProfile', '-Command',
			"Get-Process -Name '.feedss-ui-test' -ErrorAction SilentlyContinue | Stop-Process -Force",
		], { stdio: 'ignore' });
	}
	fs.mkdirSync(path.dirname(dbPath), { recursive: true });
  const binary = path.resolve('.feedss-ui-test.exe');
  const server = spawn(binary, [], {
    cwd: process.cwd(),
    detached: true,
    stdio: 'ignore',
    windowsHide: true,
    env: {
      ...process.env,
      APP_PORT: String(port),
		APP_DB_PATH: dbPath,
      APP_DISABLE_AUTO_REFRESH: 'true',
    },
  });
  server.unref();
  fs.writeFileSync(pidPath, String(server.pid));

  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/login`, { redirect: 'manual' });
      if (response.ok) return;
    } catch {
      // The server is still starting.
    }
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  throw new Error('Timed out waiting for the UI test server.');
};
