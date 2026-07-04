import { test as base, expect, type Page } from '@playwright/test';

// Helper fixtures for common console operations.
type ConsoleFixtures = {
  consolePage: Page;
};

export const test = base.extend<ConsoleFixtures>({
  consolePage: async ({ page }, use) => {
    await page.goto('/v3/console/ui');
    await expect(page.locator('h2')).toContainText('Sign in');
    await use(page);
  },
});

export { expect };

export async function login(page: Page) {
  await page.goto('/v3/console/ui');
  await page.locator('#loginUser').fill('nacos');
  await page.locator('#loginPass').fill('nacos');
  await page.locator('#loginBtn').click();
  // After login, the nav bar with tabs should appear.
  await expect(page.locator('nav')).toBeVisible({ timeout: 15_000 });
}

export async function switchTab(page: Page, tabId: string) {
  await page.locator(`nav button[data-tab="${tabId}"]`).click();
}

export function uniqueName(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 10000)}`;
}
