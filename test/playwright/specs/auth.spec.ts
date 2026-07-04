import { test, expect, login } from './helpers';

test.describe('Authentication', () => {
  test('bootstrap admin and login with nacos/nacos', async ({ page }) => {
    await login(page);
    await expect(page.locator('header h1')).toHaveText('GoNacos Console');
    await expect(page.locator('.user strong')).toHaveText('nacos');
  });

  test('logout returns to login screen', async ({ page }) => {
    await login(page);
    await page.locator('#logout').click();
    await expect(page.locator('h2')).toContainText('Sign in');
  });

  test('login with wrong password shows error', async ({ page }) => {
    await page.goto('/v3/console/ui');
    await page.locator('#loginUser').fill('nacos');
    await page.locator('#loginPass').fill('wrong-password');
    await page.locator('#loginBtn').click();
    await expect(page.locator('#loginError .error')).toBeVisible({ timeout: 10_000 });
    await expect(page.locator('nav')).toBeHidden();
  });
});
