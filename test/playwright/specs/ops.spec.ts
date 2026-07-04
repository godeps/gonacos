import { test, expect, login, switchTab } from './helpers';

test.describe('Ops and user management', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('ops backup endpoint returns data', async ({ page, request }) => {
    const token = await page.evaluate(() => localStorage.getItem('gonacos_token'));
    const res = await request.get('/v3/admin/ops/backup', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.version).toBeTruthy();
    expect(body.services).toBeTruthy();
  });

  test('ops metrics endpoint returns data', async ({ page, request }) => {
    const token = await page.evaluate(() => localStorage.getItem('gonacos_token'));
    const res = await request.get('/v3/admin/ops/metrics', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(res.ok()).toBeTruthy();
    const text = await res.text();
    expect(text).toContain('process_');
  });

  test('create user via UI', async ({ page }) => {
    await switchTab(page, 'users');
    // Wait for the initial user list to load.
    await page.waitForResponse(resp => resp.url().includes('/v3/auth/user/list'), { timeout: 10_000 });
    const username = `pwuser-${Date.now()}`;
    await page.locator('#newUser').fill(username);
    await page.locator('#newPass').fill('Test1234!');
    await page.locator('#userCreate').click();
    // Wait for the "user created" success message or error.
    await expect(page.locator('.success, .error')).toBeVisible({ timeout: 10_000 });
    // The user list table should contain the new user.
    await expect(page.locator('table tbody tr', { hasText: username })).toBeVisible({ timeout: 10_000 });
  });
});

test.describe('Responsive smoke', () => {
  test('console renders on mobile viewport', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 375, height: 667 } });
    const page = await ctx.newPage();
    await page.goto('/v3/console/ui');
    await expect(page.locator('h2')).toContainText('Sign in');
    await page.locator('#loginUser').fill('nacos');
    await page.locator('#loginPass').fill('nacos');
    await page.locator('#loginBtn').click();
    await expect(page.locator('nav')).toBeVisible({ timeout: 15_000 });
    await ctx.close();
  });
});
