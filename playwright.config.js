const { defineConfig } = require('@playwright/test');

const port = 4321;
const channel = process.env.PLAYWRIGHT_CHANNEL === ''
  ? undefined
  : (process.env.PLAYWRIGHT_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined));

module.exports = defineConfig({
  testDir: './tests/ui',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'line',
  globalSetup: require.resolve('./tests/ui/global-setup'),
  globalTeardown: require.resolve('./tests/ui/global-teardown'),
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    browserName: 'chromium',
    channel,
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'desktop',
      use: { viewport: { width: 1440, height: 900 } },
    },
    {
      name: 'mobile',
      use: {
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
      },
    },
  ],
});
