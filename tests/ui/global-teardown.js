const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const pidPath = path.resolve('.feedss-ui-test.pid');

module.exports = async () => {
  if (!fs.existsSync(pidPath)) return;
  const pid = Number(fs.readFileSync(pidPath, 'utf8'));
  if (Number.isInteger(pid) && pid > 0) {
    if (process.platform === 'win32') {
		spawnSync('powershell.exe', [
			'-NoProfile', '-Command',
			`Stop-Process -Id ${pid} -Force -ErrorAction SilentlyContinue`,
		], { stdio: 'ignore' });
    } else {
      try {
        process.kill(-pid, 'SIGTERM');
      } catch {
        // The server already exited.
      }
    }
  }
  fs.rmSync(pidPath, { force: true });
};
