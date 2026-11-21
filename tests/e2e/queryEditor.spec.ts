import { test, expect } from '@grafana/plugin-e2e';
import { column, DS_NAME, framesOf, nextQuery, pick, shot, typeCustom } from './helpers';

// The seeded analyses go back 84 days; a new panel shows the last 6 hours.
const RANGE = { from: 'now-90d', to: 'now' };

test.beforeEach(async ({ panelEditPage }) => {
  await panelEditPage.datasource.set(DS_NAME);
});

test('an empty time range is explained in the editor', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, 'demo:shop-api');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const frames = await framesOf(await response);
  // A new panel shows the last 6 hours, which holds no analysis.
  expect(column(frames[0], 'coverage')).toHaveLength(0);
  const notice = row.getByTestId('sonar-query-notice');
  await expect(notice).toContainText('No analysis in the selected time range. The latest analysis was on');
  await expect(notice).toContainText('such as the last 90 days');
  await shot(page, 'query', 'empty-time-range');

  // With a range that includes the analyses, the data appears and the hint goes.
  const wider = nextQuery(page, 'demo:shop-api');
  await panelEditPage.timeRange.set(RANGE);
  expect(column((await framesOf(await wider))[0], 'coverage')).toHaveLength(6);
  await expect(notice).toBeHidden();
});

test('a new query says what is missing, then draws history once a project is chosen', async ({ panelEditPage, page }) => {
  await panelEditPage.timeRange.set(RANGE);
  const row = panelEditPage.getQueryEditorRow('A');
  const hint = row.getByTestId('sonar-query-hint');
  await expect(hint).toHaveText('Choose at least one project.');
  // The default metric is already there.
  await expect(row.getByText('Coverage', { exact: true })).toBeVisible();
  await shot(page, 'query', 'new-query-hint');

  const response = nextQuery(page, '"queryType":"measureHistory"', 'demo:shop-api');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const frames = await framesOf(await response);
  expect(frames).toHaveLength(1);
  expect(frames[0].schema.fields[1].config?.displayNameFromDS).toBe('Coverage');
  expect(column(frames[0], 'coverage')).toHaveLength(6);
  await expect(hint).toBeHidden();

  // A second project and a second metric: one series per combination.
  let next = nextQuery(page, 'demo:shop-web');
  await pick(page, row.locator('#sonar-projects'), 'Shop Web');
  await next;
  next = nextQuery(page, '"bugs"');
  await pick(page, row.locator('#sonar-metrics'), 'Bugs');
  const four = await framesOf(await next);
  expect(four.map((f) => f.schema.fields[1].config?.displayNameFromDS)).toEqual([
    'Shop API · Coverage',
    'Shop API · Bugs',
    'Shop Web · Coverage',
    'Shop Web · Bugs',
  ]);
  await panelEditPage.setVisualization('Time series');
  await expect(page.getByText('Shop Web · Bugs').first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'history-two-projects');
});

test('current metrics for every project, as a table', async ({ panelEditPage, page }) => {
  await panelEditPage.timeRange.set(RANGE);
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, '"queryType":"measures"');
  await pick(page, row.locator('#sonar-query-type'), 'Current metrics');
  const frames = await framesOf(await response);
  expect(column(frames[0], 'project')).toEqual(['Legacy Billing', 'Shop API', 'Shop Web']);
  expect(column(frames[0], 'bugs')).toEqual([9, 1, 4]);
  // Projects are optional here, and the editor says so.
  await expect(row.locator('#sonar-projects')).toHaveAttribute('placeholder', 'All projects');
  await expect(row.getByTestId('sonar-query-hint')).toBeHidden();

  await panelEditPage.setVisualization('Table');
  // The quality gate is shown in words, not as OK/ERROR.
  await expect(page.getByText('Failed').first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText('Passed').first()).toBeVisible();
  await shot(page, 'query', 'current-metrics');
});

test('issues: filters are sent and the list is a table', async ({ panelEditPage, page }) => {
  await panelEditPage.timeRange.set(RANGE);
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"issues"');
  await pick(page, row.locator('#sonar-query-type'), 'Issues', 'Issues A list');
  let frames = await framesOf(await response);
  expect(frames[0].schema.fields.map((f) => f.name)).toEqual(
    expect.arrayContaining(['created', 'project', 'severity', 'type', 'status', 'message', 'rule', 'file', 'line', 'link'])
  );
  const all = column(frames[0], 'severity').length;
  expect(all).toBeGreaterThan(5);

  response = nextQuery(page, '"severities":["CRITICAL"]');
  await pick(page, row.locator('#sonar-severities'), 'Critical');
  frames = await framesOf(await response);
  const severities = column(frames[0], 'severity');
  expect(severities.length).toBeGreaterThan(0);
  expect(severities.length).toBeLessThan(all);
  expect(new Set(severities)).toEqual(new Set(['CRITICAL']));

  response = nextQuery(page, '"resolution":"all"');
  await row.getByRole('radio', { name: 'All', exact: true }).check({ force: true });
  await response;

  // The limit is applied on blur and capped at SonarQube's own maximum.
  response = nextQuery(page, '"limit":3');
  await row.locator('#sonar-limit').fill('3');
  await row.locator('#sonar-limit').blur();
  frames = await framesOf(await response);
  expect(column(frames[0], 'severity')).toHaveLength(3);
  response = nextQuery(page, '"limit":10000');
  await row.locator('#sonar-limit').fill('99999999');
  await row.locator('#sonar-limit').blur();
  await response;
  await expect(row.locator('#sonar-limit')).toHaveValue('10000');

  await panelEditPage.setVisualization('Table');
  await expect(page.getByText('Critical').first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'issues');
});

test('issue counts by severity and by project', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  let response = nextQuery(page, '"queryType":"issueCounts"');
  await pick(page, row.locator('#sonar-query-type'), 'Issue counts');
  let frames = await framesOf(await response);
  expect(column(frames[0], 'severity')).toEqual(['BLOCKER', 'CRITICAL', 'MAJOR', 'MINOR', 'INFO']);

  response = nextQuery(page, '"groupBy":"projects"');
  await pick(page, row.locator('#sonar-group-by'), 'Project');
  frames = await framesOf(await response);
  expect(column(frames[0], 'project')[0]).toBe('Legacy Billing');
  expect((column(frames[0], 'issues') as number[])[0]).toBeGreaterThan(5);
  await shot(page, 'query', 'issue-counts');
});

test('analyses and events need a project and list versions and quality gate changes', async ({ panelEditPage, page }) => {
  await panelEditPage.timeRange.set(RANGE);
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-query-type'), 'Analyses and events');
  await expect(row.getByTestId('sonar-query-hint')).toHaveText('Choose at least one project.');

  const response = nextQuery(page, '"queryType":"analyses"', 'demo:shop-api');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const frames = await framesOf(await response);
  expect(column(frames[0], 'version')).toEqual(['2.2', '2.1', '2.0', '1.2', '1.1', '1.0']);
  expect(column(frames[0], 'text')[0]).toBe('Version 2.2');
  expect((column(frames[0], 'text') as string[]).some((t) => t.startsWith('Quality gate: '))).toBe(true);
  await shot(page, 'query', 'analyses');
});

test('typing a project name and pressing Enter selects that project', async ({ panelEditPage, page }) => {
  await panelEditPage.timeRange.set(RANGE);
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, '"projects":["demo:shop-web"]');
  const projects = row.locator('#sonar-projects');
  await projects.click();
  await projects.fill('shop web');
  // Enter takes the first option, "Use custom value"; the editor resolves the
  // name to the project's key instead of sending the name.
  await page.keyboard.press('Enter');
  const frames = await framesOf(await response);
  expect(column(frames[0], 'coverage')).toHaveLength(4);
  await expect(row.getByText('Shop Web', { exact: true }).first()).toBeVisible();
});

test("SonarQube's own error message reaches the panel", async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  const response = nextQuery(page, 'no:such-project');
  // A key that is not in the list can be typed.
  await typeCustom(page, row.locator('#sonar-projects'), 'no:such-project');
  const body = await (await response).json();
  expect(body.results.A.error).toContain("SonarQube: Component key 'no:such-project' not found");
  expect(body.results.A.errorSource).toBe('downstream');
  await expect(page.getByText(/Component key 'no:such-project' not found/).first()).toBeVisible({ timeout: 15_000 });
  await shot(page, 'query', 'unknown-project');
});

test('a branch is sent when given', async ({ panelEditPage, page }) => {
  const row = panelEditPage.getQueryEditorRow('A');
  await pick(page, row.locator('#sonar-projects'), 'Shop API');
  const response = nextQuery(page, '"branch":"release/1.0"');
  await row.locator('#sonar-branch').fill(' release/1.0 ');
  await row.locator('#sonar-branch').blur();
  const body = await (await response).json();
  // Community Build has no branch analysis, so the server says the branch does not exist.
  expect(body.results.A.error).toContain("on branch 'release/1.0' not found");
});
