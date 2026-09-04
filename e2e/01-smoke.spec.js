const { test, expect } = require('@playwright/test');
const { startApp, click, api } = require('./helpers');

test('frontend boots and talks to the backend', async ({ page, request }) => {
  const errors = await startApp(page);

  await expect(page.locator('canvas')).toBeVisible();
  expect(errors, 'uncaught page errors').toEqual([]);

  const status = await api(request, '/api/system/status');
  expect(status).toHaveProperty('public_ip');

  // Fyne sets the document title from the window title once it is running.
  expect(await page.title()).toBe('AmneziaWG Web UI');

  await page.screenshot({ path: 'shots/01-loaded.png' });
});
