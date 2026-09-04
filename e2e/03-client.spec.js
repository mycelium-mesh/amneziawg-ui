const { test, expect } = require('@playwright/test');
const { startApp, click, type, api } = require('./helpers');

test('add a client and open its config', async ({ page, request }) => {
  const errors = await startApp(page);

  const [server] = await api(request, '/api/servers');
  expect(server, 'the create spec must run first').toBeTruthy();

  // "Add client" on the server card.
  await click(page, 225, 211);
  await page.waitForTimeout(1200);
  await page.screenshot({ path: 'shots/06-client-dialog.png' });

  await click(page, 800, 233);
  await type(page, 'laptop');
  await click(page, 807, 751);

  await expect.poll(async () => (await api(request, `/api/servers/${server.id}/clients`)).length,
    { timeout: 60_000, intervals: [1000] }).toBe(1);

  const [client] = await api(request, `/api/servers/${server.id}/clients`);
  expect(client.name).toBe('laptop');
  expect(client.client_ip).toMatch(/^10\.0\.0\./);

  await page.waitForTimeout(4000);
  await page.screenshot({ path: 'shots/07-client-added.png' });
  expect(errors, 'uncaught page errors').toEqual([]);
});
