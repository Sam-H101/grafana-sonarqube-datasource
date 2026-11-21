import { test, expect } from '@grafana/plugin-e2e';
import { CLOUD, column, DS_NAME, framesOf, nextQuery, pick, shot, typeCustom } from './helpers';

// Quality gate conditions, security hotspots, pull requests and project tags.
// The dev stack's SonarQube is Community Build 26.11, which has no hotspot
// rules and no pull request analysis; those two are shown with data from
// SonarQube Cloud when SONARCLOUD_TESTS=1.

const CLOUD_DS = 'SonarQube Cloud (public)';
const CLOUD_PROJECT = 'SonarSource_sonarqube-mcp-server';

test('quality gate conditions say why a gate failed', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"qualityGate"');
  await pick(page, row.locator('#sonar-query-type'), 'Quality gate conditions');
  let frames = await framesOf(await response);
  // Projects are optional: every project's conditions, failed ones first.
  const projects = column(frames[0], 'project') as string[];
  expect(new Set(projects)).toEqual(new Set(['Legacy Billing', 'Shop API', 'Shop Web']));
  const statuses = column(frames[0], 'status') as string[];
  expect(statuses).toContain('OK');
  const failed = statuses.filter((s) => s === 'ERROR').length;
  expect(failed).toBeGreaterThan(0);
  const first = projects.indexOf('Legacy Billing');
  expect(statuses[first]).toBe('ERROR');
  expect(column(frames[0], 'quality_gate')[first]).toBe('ERROR');
  expect(column(frames[0], 'required')[first]).toBe('at most 0');
  expect(column(frames[0], 'condition')[first]).toBe('New Issues');

  response = nextQuery(page, '"failedOnly":true');
  await row.locator('#sonar-failed-only').check({ force: true });
  frames = await framesOf(await response);
  expect(column(frames[0], 'status')).toEqual(Array(failed).fill('ERROR'));
  expect(column(frames[0], 'project')).not.toContain('Shop API');

  await panelEditPage.setVisualization('Table');
  await expect(page.getByText('at most 0').first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText('Failed').first()).toBeVisible();
  await shot(page, 'more', 'quality-gate-conditions');
});

test('projects can be limited to a tag', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"projects"');
  await pick(page, row.locator('#sonar-query-type'), 'Projects');
  expect(column((await framesOf(await response))[0], 'name')).toEqual(['Legacy Billing', 'Shop API', 'Shop Web']);

  // The dropdown offers the tags the projects have.
  response = nextQuery(page, '"tags":["backend"]');
  await pick(page, row.locator('#sonar-tags'), 'backend');
  let frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toEqual(['Legacy Billing', 'Shop API']);

  // A second tag widens the list: projects with either tag.
  response = nextQuery(page, '"tags":["backend","frontend"]');
  await pick(page, row.locator('#sonar-tags'), 'frontend');
  frames = await framesOf(await response);
  expect(column(frames[0], 'name')).toEqual(['Legacy Billing', 'Shop API', 'Shop Web']);
  await shot(page, 'more', 'projects-by-tag');
});

test('a tag that no project has is explained', async ({ page }) => {
  const resp = await page.request.post('/api/ds/query', {
    data: { from: 'now-1h', to: 'now', queries: [{ refId: 'A', datasource: { uid: 'sonarqube-dev' }, queryType: 'projects', tags: ['no-such-tag'] }] },
  });
  const frame = (await resp.json()).results.A.frames[0];
  expect(column(frame, 'name')).toEqual([]);
  expect(frame.schema.meta.notices[0].text).toBe('No project the token can see has the tag no-such-tag.');
});

test('security hotspots need a project, and an empty list is explained', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-query-type'), 'Security hotspots');
  await expect(row.getByTestId('sonar-query-hint')).toHaveText('Choose at least one project.');

  let response = nextQuery(page, '"queryType":"hotspots"', '"hotspotStatus":"toReview"', 'demo:shop-web');
  await pick(page, row.locator('#sonar-projects'), 'Shop Web');
  const frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(
    expect.arrayContaining(['created', 'project', 'priority', 'category', 'status', 'resolution', 'message', 'rule', 'file', 'line', 'link'])
  );
  // Community Build 26.11 reports the former hotspot rules as vulnerabilities.
  expect(column(frames[0], 'message')).toEqual([]);
  await expect(row.getByTestId('sonar-query-notice')).toContainText('No matching security hotspots.');
  await expect(row.getByTestId('sonar-query-notice')).toContainText('the Issues query lists');

  response = nextQuery(page, '"hotspotStatus":"all"');
  await row.getByRole('radio', { name: 'All', exact: true }).check({ force: true });
  await framesOf(await response);
  await shot(page, 'more', 'hotspots-none');
});

test('pull requests: a server without them says what is needed', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-query-type'), 'Pull requests');
  await expect(row.getByTestId('sonar-query-hint')).toHaveText('Choose at least one project.');
  // This type has no branch or pull request of its own.
  await expect(row.locator('#sonar-branch')).toBeHidden();

  const response = nextQuery(page, '"queryType":"pullRequests"', 'demo:shop-api');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const body = await (await response).json();
  expect(body.results.A.error).toBe(
    'This SonarQube does not analyse pull requests. That needs SonarQube Developer Edition or higher, or SonarQube Cloud.'
  );
  expect(body.results.A.errorSource).toBe('downstream');
  await expect(page.getByText(/does not analyse pull requests/).first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'more', 'pull-requests-unsupported');
});

test('a pull request is sent instead of a branch, never with one', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const response = nextQuery(page, '"pullRequest":"#12"');
  await row.locator('#sonar-pull-request').fill(' #12 ');
  await row.locator('#sonar-pull-request').blur();
  const body = await (await response).json();
  // Community Build has no pull requests, so the server says this one does not exist.
  expect(body.results.A.error).toContain("of pull request '12' not found");

  await row.locator('#sonar-branch').fill('main');
  await row.locator('#sonar-branch').blur();
  await expect(row.getByTestId('sonar-query-hint')).toHaveText('Choose a branch or a pull request, not both.');
  await shot(page, 'more', 'branch-and-pull-request');
});

test('SonarQube Cloud: pull requests, a pull request’s metrics, and hotspots', async ({ panelEditPage, page }) => {
  test.skip(!CLOUD, 'set SONARCLOUD_TESTS=1 to test against sonarcloud.io');
  await panelEditPage.datasource.set(CLOUD_DS);
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-query-type'), 'Pull requests');
  let response = nextQuery(page, '"queryType":"pullRequests"', CLOUD_PROJECT);
  await typeCustom(page, row.locator('#sonar-projects'), CLOUD_PROJECT);
  let frames = await framesOf(await response);
  const numbers = column(frames[0], 'key') as string[];
  expect(numbers.length).toBeGreaterThan(0);
  expect(column(frames[0], 'link')[0]).toBe(`https://sonarcloud.io/dashboard?id=${CLOUD_PROJECT}&pullRequest=${numbers[0]}`);
  expect(['OK', 'ERROR', 'NONE', '']).toContain(column(frames[0], 'quality_gate')[0]);
  await panelEditPage.setVisualization('Table');
  await expect(page.getByText(String(column(frames[0], 'title')[0])).first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'more', 'cloud-pull-requests');

  // The most recently analysed pull request has current metrics of its own.
  response = nextQuery(page, '"queryType":"measures"');
  await pick(page, row.locator('#sonar-query-type'), 'Current metrics');
  await response;
  response = nextQuery(page, `"pullRequest":"${numbers[0]}"`);
  await row.locator('#sonar-pull-request').fill(numbers[0]);
  await row.locator('#sonar-pull-request').blur();
  frames = await framesOf(await response);
  expect(column(frames[0], 'key')).toEqual([CLOUD_PROJECT]);

  response = nextQuery(page, '"queryType":"hotspots"');
  await pick(page, row.locator('#sonar-query-type'), 'Security hotspots');
  await response;
  await row.locator('#sonar-pull-request').fill('');
  await row.locator('#sonar-pull-request').blur();
  response = nextQuery(page, '"hotspotStatus":"all"');
  await row.getByRole('radio', { name: 'All', exact: true }).check({ force: true });
  frames = await framesOf(await response);
  expect(column(frames[0], 'message').length).toBeGreaterThan(0);
  expect(String(column(frames[0], 'link')[0])).toContain(`https://sonarcloud.io/security_hotspots?id=${CLOUD_PROJECT}&hotspots=`);
  await shot(page, 'more', 'cloud-hotspots');
});
