import { test, expect } from '@grafana/plugin-e2e';
import type { Page } from '@playwright/test';
import { CLOUD, shot, SONAR_URL, token, TYPE } from './helpers';

async function chooseDeployment(page: Page, name: 'SonarQube Server' | 'SonarQube Cloud' | 'SonarQube Cloud (US)') {
  await page.getByRole('radio', { name, exact: true }).check({ force: true });
}

test('the plugin is installed and listed', async ({ page }) => {
  await page.goto(`/plugins/${TYPE}`);
  await expect(page.getByRole('heading', { name: 'SonarQube', exact: true })).toBeVisible({ timeout: 15_000 });
});

test('provisioned data source: Save & test reports the version and the visible projects', async ({
  gotoDataSourceConfigPage,
  page,
}) => {
  const configPage = await gotoDataSourceConfigPage('sonarqube-dev');
  // The first page load after Grafana starts can take more than 5 s.
  await expect(page.locator('#sonar-url')).toHaveValue(SONAR_URL, { timeout: 15_000 });
  await expect(page.getByRole('radio', { name: 'SonarQube Server', exact: true })).toBeChecked();
  // A saved token is write-only: it is never sent back to the browser.
  await expect(page.locator('#sonar-token')).toBeDisabled();
  await expect(page.locator('#sonar-token')).toHaveValue('configured');
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', {
    hasText: /Connected to SonarQube Server \d+\.\d+[\d.]*\. The token can see 3 projects\./,
  });
  await shot(page, 'config', 'provisioned-ok');
});

test('a new data source: no token, a wrong token, then the right one', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.locator('#sonar-url').fill(SONAR_URL);

  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'This SonarQube requires authentication. Enter a user token' });
  await shot(page, 'config', 'token-required');

  // Edits made right after Save & test can be undone by Grafana reloading the
  // saved settings, so each step repeats until its expected outcome is seen.
  const tokenInput = page.locator('#sonar-token');
  const setToken = async (value: string) => {
    if (await tokenInput.isDisabled()) {
      await page.getByRole('button', { name: 'Reset' }).first().click();
    }
    await expect(tokenInput).toBeEditable();
    await tokenInput.fill(value);
  };

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await setToken('squ_0000000000000000000000000000000000000000');
    await expect(configPage.saveAndTest()).not.toBeOK();
    await expect(configPage).toHaveAlert('error', { hasText: 'SonarQube rejected the token.' });
  }).toPass({ timeout: 30_000 });
  await expect(page.getByText('My Account > Security').first()).toBeVisible();
  // The token goes over http:// here, and the page says so.
  await expect(page.getByText('Token over plain HTTP')).toBeVisible();
  await shot(page, 'config', 'wrong-token');

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await setToken(token());
    await expect(configPage.saveAndTest()).toBeOK();
  }).toPass({ timeout: 30_000 });
  await expect(configPage).toHaveAlert('success', { hasText: 'The token can see 3 projects.' });
});

test('a wrong address is explained', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });

  // A wrong path prefix: the server answers with a web page, not the API.
  await page.locator('#sonar-url').fill('http://sonarqube:9000/wrong-prefix');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'did not answer like SonarQube' });
  await expect(page.getByText('including any path prefix such as /sonarqube').first()).toBeVisible();
  await shot(page, 'config', 'not-sonarqube');

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await page.locator('#sonar-url').fill('http://sonarqube:9999');
    await expect(configPage.saveAndTest()).not.toBeOK();
    await expect(configPage).toHaveAlert('error', { hasText: 'Cannot reach SonarQube at http://sonarqube:9999' });
  }).toPass({ timeout: 30_000 });
  // The message points users of a hosted Grafana at the private-network option.
  await expect(page.getByText('turn on Private data source connect').first()).toBeVisible();
  await shot(page, 'config', 'unreachable');

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await page.locator('#sonar-url').fill('sonarqube.example.com');
    await expect(configPage.saveAndTest()).not.toBeOK();
    await expect(configPage).toHaveAlert('error', { hasText: 'must start with http:// or https://' });
  }).toPass({ timeout: 30_000 });
});

test('choosing SonarQube Cloud sets the address and asks for the organization', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  // The first page load after Grafana starts can take more than 5 s.
  await expect(page.locator('#sonar-url')).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('#sonar-organization')).toBeHidden();
  // Server-only sections are shown for a server.
  await expect(page.getByRole('heading', { name: 'TLS' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Private network' })).toBeVisible();

  await chooseDeployment(page, 'SonarQube Cloud');
  await expect(page.locator('#sonar-url')).toHaveValue('https://sonarcloud.io');
  await expect(page.locator('#sonar-url')).toBeDisabled();
  await expect(page.locator('#sonar-organization')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'TLS' })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Private network' })).toBeHidden();
  await shot(page, 'config', 'cloud');

  await chooseDeployment(page, 'SonarQube Cloud (US)');
  await expect(page.locator('#sonar-url')).toHaveValue('https://sonarqube.us');

  // Without an organization the problem is named before any request is made.
  await chooseDeployment(page, 'SonarQube Cloud');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'SonarQube Cloud needs an organization key' });

  // Back to a server: the Cloud address is cleared for the user to type theirs.
  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await chooseDeployment(page, 'SonarQube Server');
    await expect(page.locator('#sonar-url')).toHaveValue('');
    await expect(page.locator('#sonar-url')).toBeEditable();
  }).toPass({ timeout: 30_000 });
});

test('SonarQube Cloud: anonymous access to an organization, and a wrong organization', async ({
  createDataSourceConfigPage,
  page,
}) => {
  test.skip(!CLOUD, 'set SONARCLOUD_TESTS=1 to test against sonarcloud.io');
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await chooseDeployment(page, 'SonarQube Cloud');
  await page.locator('#sonar-organization').fill('no-such-organization-zzz');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: "SonarQube Cloud: No organization for key 'no-such-organization-zzz'" });

  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await page.locator('#sonar-organization').fill('sonarsource');
    await expect(configPage.saveAndTest()).toBeOK();
  }).toPass({ timeout: 45_000 });
  await expect(configPage).toHaveAlert('success', {
    hasText: /Connected to SonarQube Cloud, organization sonarsource\. Anonymous access can see \d+ projects\. Only public projects/,
  });
  await shot(page, 'config', 'cloud-ok');
});
