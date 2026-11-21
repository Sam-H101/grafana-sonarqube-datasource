import { test, expect } from '@grafana/plugin-e2e';
import { column, shot, SONAR_PRIVATE_URL, token, TYPE } from './helpers';

// The dev stack's Grafana has the secure socks proxy turned on, pointing at a
// plain SOCKS5 proxy (docker-compose.yaml). That is the mechanism behind
// Grafana Cloud's Private data source connect. "sonarqube-private" resolves
// only on a network Grafana is not on, so a request that reaches it went
// through the proxy.

test('a private SonarQube is reachable only with the secure socks proxy on', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.locator('#sonar-url').fill(SONAR_PRIVATE_URL);
  await page.locator('#sonar-token').fill(token());

  // Directly, Grafana cannot even resolve the name.
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: `Cannot reach SonarQube at ${SONAR_PRIVATE_URL}: host not found.` });
  await expect(page.getByText('turn on Private data source connect').first()).toBeVisible();
  await shot(page, 'private-network', 'direct-fails');

  // With the proxy on, the same settings connect. The switch is Grafana's own
  // control for this; repeat the edit until the saved setting takes effect.
  const proxySwitch = page.getByRole('heading', { name: 'Private network' }).locator('xpath=ancestor::section[1]').getByRole('switch');
  await page.waitForLoadState('networkidle');
  await expect(async () => {
    if (!(await proxySwitch.isChecked())) {
      await proxySwitch.check({ force: true });
    }
    await expect(configPage.saveAndTest()).toBeOK();
  }).toPass({ timeout: 45_000 });
  await expect(configPage).toHaveAlert('success', { hasText: 'The token can see 3 projects.' });
  await shot(page, 'private-network', 'proxy-ok');
});

test('queries on the provisioned private data source go through the proxy', async ({ page }) => {
  const health = await page.request.get('/api/datasources/uid/sonarqube-pdc/health');
  expect(health.ok()).toBeTruthy();
  expect((await health.json()).message).toContain('Connected to SonarQube Server');

  const now = Date.now();
  const resp = await page.request.post('/api/ds/query', {
    data: {
      from: String(now - 90 * 24 * 3600 * 1000),
      to: String(now),
      queries: [
        {
          refId: 'A',
          datasource: { type: TYPE, uid: 'sonarqube-pdc' },
          queryType: 'measureHistory',
          projects: ['demo:shop-api'],
          metrics: ['bugs'],
        },
      ],
    },
  });
  expect(resp.ok()).toBeTruthy();
  const frame = (await resp.json()).results.A.frames[0];
  expect(column(frame, 'bugs')).toEqual([6, 5, 7, 3, 2, 1]);

  // The editor's dropdown lists load through it as well.
  const projects = await page.request.get('/api/datasources/uid/sonarqube-pdc/resources/projects');
  expect((await projects.json()).total).toBe(3);
});
