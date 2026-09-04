const { test, expect } = require('@playwright/test');
const { startApp, click, type, scroll, api } = require('./helpers');

test('create a server through the UI', async ({ page, request }) => {
  const errors = await startApp(page);

  // Open the "Create New VPN Server" accordion.
  await click(page, 130, 87);
  await page.screenshot({ path: 'shots/02-form.png' });

  // Fill in the name; every other field already has a usable default.
  await click(page, 500, 131);
  await type(page, 'E2E Server');
  await page.screenshot({ path: 'shots/03-named.png' });

  // Scroll to the very bottom, where the submit button sits below the
  // obfuscation block.
  await scroll(page, 750, 500, 3000);
  await page.screenshot({ path: 'shots/04-scrolled.png' });
  await click(page, 87, 835);

  await expect.poll(async () => (await api(request, '/api/servers')).length,
    { timeout: 60_000, intervals: [1000] }).toBe(1);

  const [server] = await api(request, '/api/servers');
  expect(server.name).toBe('E2E Server');
  expect(server.obfuscation_enabled).toBe(true);
  expect(server.obfuscation_params.RandomTrailers).toBe(true);
  expect(server.obfuscation_params.HeaderProtectionKey).not.toBe('');

  await page.waitForTimeout(4000);
  await page.screenshot({ path: 'shots/05-created.png' });
  expect(errors, 'uncaught page errors').toEqual([]);
});
