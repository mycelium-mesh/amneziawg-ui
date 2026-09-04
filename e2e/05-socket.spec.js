const { test, expect } = require('@playwright/test');
const { startApp } = require('./helpers');

// Regression guard: compressing the Socket.IO long-polling responses used to
// tear the engine.io session down on every event, so the client re-handshook
// every couple of seconds instead of holding one session open.
test('the Socket.IO session stays up while events flow', async ({ page }) => {
  const handshakes = [];
  const polls = [];

  page.on('request', (request) => {
    const url = request.url();
    if (!url.includes('/socket.io/')) return;
    (url.includes('sid=') ? polls : handshakes).push(url);
  });

  await startApp(page);
  await page.waitForTimeout(40_000);

  expect(polls.length, 'no polling traffic at all').toBeGreaterThan(2);
  expect(handshakes.length, `re-handshaking: ${handshakes.length} sessions in 40s`).toBeLessThanOrEqual(2);
});
