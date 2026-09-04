const { defineConfig } = require('@playwright/test');

// The UI is a WebAssembly canvas app: nothing here can be selected by CSS, so
// the tests drive it by coordinates and assert against the REST API.
module.exports = defineConfig({
  testDir: '.',
  // The specs share one backend and build on each other's state.
  fullyParallel: false,
  workers: 1,
  timeout: 180_000,
  expect: { timeout: 60_000 },
  reporter: [['list']],
  use: {
    baseURL: process.env.AWG_URL || 'http://localhost:51836',
    httpCredentials: { username: 'admin', password: 'changeme' },
    viewport: { width: 1500, height: 950 },
    // Reuse the Chrome that is already installed instead of Playwright's own
    // download; the app needs a WebGL-capable build either way.
    channel: 'chrome',
    launchOptions: { args: ['--enable-unsafe-swiftshader'] },
    screenshot: 'off',
    video: 'off',
  },
});
