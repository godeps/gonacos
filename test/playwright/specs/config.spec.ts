import { test, expect, login, switchTab, uniqueName } from './helpers';

test.describe('Config management', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
    await switchTab(page, 'configs');
  });

  test('publish and list a config', async ({ page }) => {
    const dataId = uniqueName('cfg');
    const content = `{"key":"playwright"}`;

    await page.locator('#pubNs').selectOption('public');
    await page.locator('#pubGroup').fill('DEFAULT_GROUP');
    await page.locator('#pubDataId').fill(dataId);
    await page.locator('#pubType').fill('json');
    await page.locator('#pubContent').fill(content);
    await page.locator('#pubBtn').click();

    // Verify the config appears in the list.
    await expect(page.locator('table tr', { hasText: dataId })).toBeVisible({ timeout: 10_000 });
  });

  test('refresh config list', async ({ page }) => {
    await page.locator('#cfgRefresh').click();
    await expect(page.locator('table')).toBeVisible();
  });
});

test.describe('Config API backing state', () => {
  test('published config is queryable via API', async ({ request, page }) => {
    await login(page);
    const token = await page.evaluate(() => localStorage.getItem('gonacos_token'));
    expect(token).toBeTruthy();

    const dataId = uniqueName('api-cfg');
    const group = 'DEFAULT_GROUP';
    const content = `{"from":"playwright"}`;

    // Publish via API.
    const pubRes = await request.post('/v3/admin/cs/config', {
      headers: { Authorization: `Bearer ${token}` },
      form: { dataId, groupName: group, content, type: 'json', namespaceId: 'public' },
    });
    expect(pubRes.ok()).toBeTruthy();

    // Query via API.
    const queryRes = await request.get('/v3/admin/cs/config', {
      params: { dataId, groupName: group, namespaceId: 'public' },
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(queryRes.ok()).toBeTruthy();
    const body = await queryRes.json();
    expect(body.data.content).toBe(content);
  });
});
