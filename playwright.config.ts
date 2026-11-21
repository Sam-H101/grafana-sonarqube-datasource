import type { PluginOptions } from '@grafana/plugin-e2e';
import { defineConfig } from '@playwright/test';
import baseConfig from './.config/playwright.config';

/**
 * See https://playwright.dev/docs/test-configuration.
 */
export default defineConfig<PluginOptions>(baseConfig, {
  // Browser tests live in tests/e2e; tests/integration holds Go tests.
  testDir: './tests/e2e',
  // The toolbox has no display to open the HTML report on.
  reporter: [['list'], ['html', { open: 'never' }]],
});
