const { expect } = require('@playwright/test');

// startApp loads the page and waits until the Fyne canvas is up and the app
// has fetched its first data from the backend.
async function startApp(page) {
  const errors = [];
  page.on('pageerror', (err) => errors.push(String(err)));

  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('canvas', { timeout: 120_000 });
  // "fyne package" generates the loader page: it hides #main as soon as Fyne
  // puts its canvas up.
  await page.waitForFunction(() => document.getElementById('main')?.style.display === 'none', null,
    { timeout: 120_000 });
  await page.waitForTimeout(3000);
  return errors;
}

// click moves the pointer first: the Fyne web driver taps wherever its last
// known cursor position is, so a bare click would land in the wrong place.
async function click(page, x, y) {
  await page.mouse.move(x, y);
  await page.waitForTimeout(150);
  await page.mouse.click(x, y);
  await page.waitForTimeout(600);
}

async function type(page, text) {
  await page.keyboard.type(text, { delay: 25 });
  await page.waitForTimeout(300);
}

async function scroll(page, x, y, delta) {
  await page.mouse.move(x, y);
  await page.mouse.wheel(0, delta);
  await page.waitForTimeout(800);
}

// api returns parsed JSON from the backend, using the same credentials as the
// browser context.
async function api(request, path) {
  const response = await request.get(path);
  expect(response.ok(), `${path} -> ${response.status()}`).toBeTruthy();
  return response.json();
}

module.exports = { startApp, click, type, scroll, api };
