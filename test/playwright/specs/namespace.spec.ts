import { test, expect, login, switchTab, uniqueName } from './helpers';

test.describe('Namespace management', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
    await switchTab(page, 'namespaces');
  });

  test('public namespace is visible and non-deletable', async ({ page }) => {
    await expect(page.locator('table')).toBeVisible();
    const publicRow = page.locator('tr', { hasText: 'public' }).first();
    await expect(publicRow).toBeVisible();
    await expect(publicRow.locator('.badge')).toHaveText('system');
  });

  test('create and delete a custom namespace', async ({ page }) => {
    const nsId = uniqueName('ns');
    const nsName = `Test NS ${Date.now()}`;

    await page.locator('#nsId').fill(nsId);
    await page.locator('#nsName').fill(nsName);
    await page.locator('#nsDesc').fill('playwright test namespace');
    await page.locator('#nsCreate').click();

    // Wait for the table to refresh and show the new namespace.
    await expect(page.locator('tr', { hasText: nsId })).toBeVisible({ timeout: 10_000 });

    // Delete it.
    await page.locator(`button[data-del-ns="${nsId}"]`).click();
    await expect(page.locator('tr', { hasText: nsId })).toHaveCount(0, { timeout: 10_000 });
  });
});
