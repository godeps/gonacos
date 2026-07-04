import { test, expect, login, switchTab } from './helpers';

test.describe('Service management', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
    await switchTab(page, 'services');
  });

  test('service list loads', async ({ page }) => {
    await expect(page.locator('table')).toBeVisible();
    await expect(page.locator('th')).toContainText(['Name', 'Group']);
  });

  test('refresh service list', async ({ page }) => {
    await page.locator('#svcRefresh').click();
    await expect(page.locator('table')).toBeVisible();
  });
});

test.describe('Service API backing state', () => {
  test('registered service is listable via API', async ({ request, page }) => {
    await login(page);
    const token = await page.evaluate(() => localStorage.getItem('gonacos_token'));

    const serviceName = `pw-svc-${Date.now()}`;
    const ip = '10.99.0.1';
    const port = 9090;

    const regRes = await request.post('/v3/admin/ns/instance', {
      headers: { Authorization: `Bearer ${token}` },
      form: {
        serviceName,
        groupName: 'DEFAULT_GROUP',
        namespaceId: 'public',
        ip,
        port: String(port),
        clusterName: 'DEFAULT',
        ephemeral: 'true',
        weight: '1',
        enabled: 'true',
        healthy: 'true',
      },
    });
    expect(regRes.ok()).toBeTruthy();

    const listRes = await request.get('/v3/admin/ns/instance/list', {
      params: { serviceName, groupName: 'DEFAULT_GROUP', namespaceId: 'public' },
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(listRes.ok()).toBeTruthy();
    const body = await listRes.json();
    const instances = body.data?.list || [];
    const found = instances.some((i: any) => i.ip === ip && i.port === port);
    expect(found).toBeTruthy();
  });
});
