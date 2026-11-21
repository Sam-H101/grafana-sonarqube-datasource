/** Which SonarQube a URL points at. Mirrors IsCloudHost in pkg/models/settings.go. */
export type Deployment = 'server' | 'cloud-eu' | 'cloud-us';

export const CLOUD_REGIONS: Record<Exclude<Deployment, 'server'>, string> = {
  'cloud-eu': 'https://sonarcloud.io',
  'cloud-us': 'https://sonarqube.us',
};

const CLOUD_HOSTS: Record<string, Deployment> = {
  'sonarcloud.io': 'cloud-eu',
  'www.sonarcloud.io': 'cloud-eu',
  'sonarqube.us': 'cloud-us',
  'www.sonarqube.us': 'cloud-us',
};

export function deploymentOf(url?: string): Deployment {
  const raw = (url ?? '').trim();
  if (!raw) {
    return 'server';
  }
  try {
    const host = new URL(raw).hostname.toLowerCase().replace(/\.$/, '');
    return CLOUD_HOSTS[host] ?? 'server';
  } catch {
    return 'server';
  }
}
