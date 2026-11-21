import { test, expect } from '@grafana/plugin-e2e';
import type { Response } from '@playwright/test';
import { CLOUD, column, shot, TYPE } from './helpers';

interface Seen {
  body: string;
  result: { error?: string; frames?: Array<{ schema: { fields: Array<{ name: string }> }; data: { values: unknown[][] } }> };
}

/** Collects every query result of the page, by query type. */
function watchQueries(page: import('@playwright/test').Page): Seen[] {
  const seen: Seen[] = [];
  page.on('response', async (r: Response) => {
    if (!r.url().includes('/api/ds/query')) {
      return;
    }
    const body = r.request().postData() ?? '';
    try {
      const json = await r.json();
      for (const result of Object.values(json.results ?? {})) {
        seen.push({ body, result: result as Seen['result'] });
      }
    } catch {
      // The page navigated away before the body was read.
    }
  });
  return seen;
}

test('the provisioned dashboard draws every panel from SonarQube', async ({ gotoDashboardPage, page }) => {
  const seen = watchQueries(page);
  await gotoDashboardPage({ uid: 'sonarqube-overview' });

  // The table of projects, with the quality gate in words.
  await expect(page.getByText('Legacy Billing').first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText('Failed').first()).toBeVisible();
  await expect(page.getByText('Passed').first()).toBeVisible();

  // Panels further down load when scrolled to.
  for (const title of ['Coverage', 'Bugs and vulnerabilities', 'Open issues by severity', 'Open issues by type', 'Releases and quality gate changes', 'Newest open issues']) {
    const heading = page.getByRole('heading', { name: title, exact: true }).first();
    await heading.scrollIntoViewIfNeeded();
    await expect(heading).toBeVisible();
  }
  for (const type of ['measures', 'measureHistory', 'issueCounts', 'analyses', 'issues']) {
    await expect.poll(() => seen.filter((s) => s.body.includes(`"queryType":"${type}"`)).length, { timeout: 30_000 }).toBeGreaterThan(0);
  }
  const errors = seen.filter((s) => s.result.error).map((s) => s.result.error);
  expect(errors).toEqual([]);
  // Every query returned rows.
  for (const s of seen) {
    const rows = (s.result.frames ?? []).reduce((n, f) => n + (f.data.values[0]?.length ?? 0), 0);
    expect(rows, s.body.slice(0, 200)).toBeGreaterThan(0);
  }
  // The "All" value of the project variable expanded to the three projects.
  const history = seen.find((s) => s.body.includes('"queryType":"measureHistory"'))!;
  for (const key of ['demo:legacy-billing', 'demo:shop-api', 'demo:shop-web']) {
    expect(history.body).toContain(key);
  }
  // The annotation query ran and found the seeded releases.
  const annotations = seen.find((s) => s.body.includes('"refId":"Anno"'));
  expect(annotations, 'the annotation query').toBeTruthy();
  expect(column(annotations!.result.frames![0], 'text')).toContain('Version 2.2');

  await page.getByText('Legacy Billing').first().scrollIntoViewIfNeeded();
  await shot(page, 'dashboard', 'overview');
});

test('the project variable lists projects by name and filters the panels', async ({ gotoDashboardPage, page }) => {
  await gotoDashboardPage({ uid: 'sonarqube-overview', queryParams: new URLSearchParams({ 'var-project': 'demo:shop-api' }) });
  await expect(page.getByText('Shop API').first()).toBeVisible({ timeout: 30_000 });
  // The variable shows the project's name, though its value is the key.
  await expect(page.getByTestId('data-testid Dashboard template variables submenu Label Project').locator('..')).toContainText('Shop API');

  const now = Date.now();
  const resp = await page.request.post('/api/ds/query', {
    data: {
      from: String(now - 3600_000),
      to: String(now),
      queries: [{ refId: 'A', datasource: { type: TYPE, uid: 'sonarqube-dev' }, queryType: 'projects' }],
    },
  });
  const frame = (await resp.json()).results.A.frames[0];
  expect(column(frame, 'name')).toEqual(['Legacy Billing', 'Shop API', 'Shop Web']);
  expect(column(frame, 'key')).toEqual(['demo:legacy-billing', 'demo:shop-api', 'demo:shop-web']);

  // With one project selected the table has only that project's row.
  const table = page.getByRole('heading', { name: 'Projects', exact: true }).first().locator('xpath=ancestor::section[1]');
  await expect(table.getByText('Shop API')).toBeVisible();
  await expect(table.getByText('Legacy Billing')).toBeHidden();
  await shot(page, 'dashboard', 'one-project');
});

test('the provisioned alert rule evaluates on the server and fires for low coverage', async ({ page }) => {
  test.setTimeout(150_000);
  let rule: { state?: string; alerts?: Array<{ labels: Record<string, string>; state: string }> } | undefined;
  await expect
    .poll(
      async () => {
        const resp = await page.request.get('/api/prometheus/grafana/api/v1/rules');
        const groups = (await resp.json()).data?.groups ?? [];
        rule = groups.flatMap((g: { rules: Array<{ name: string }> }) => g.rules).find((r: { name: string }) => r.name === 'Coverage below 50%');
        return rule?.state;
      },
      { timeout: 120_000, intervals: [3000] }
    )
    .toBe('firing');
  // Legacy Billing (about 10% coverage) fires; Shop API (about 80%) does not.
  const firing = (rule!.alerts ?? []).filter((a) => a.state.toLowerCase().includes('alerting')).map((a) => a.labels.project);
  expect(firing).toEqual(['Legacy Billing']);
});

test('SonarQube Cloud: the provisioned public data source answers queries', async ({ page }) => {
  test.skip(!CLOUD, 'set SONARCLOUD_TESTS=1 to test against sonarcloud.io');
  const health = await page.request.get('/api/datasources/uid/sonarcloud-public/health');
  expect((await health.json()).message).toMatch(/^Connected to SonarQube Cloud, organization sonarsource\./);

  const now = Date.now();
  const resp = await page.request.post('/api/ds/query', {
    data: {
      from: String(now - 365 * 24 * 3600 * 1000),
      to: String(now),
      queries: [
        {
          refId: 'A',
          datasource: { type: TYPE, uid: 'sonarcloud-public' },
          queryType: 'measureHistory',
          projects: ['SonarSource_echoes-react'],
          metrics: ['coverage'],
          branch: 'main',
        },
      ],
    },
  });
  expect(resp.ok()).toBeTruthy();
  const frame = (await resp.json()).results.A.frames[0];
  expect(column(frame, 'coverage').length).toBeGreaterThan(5);
});
