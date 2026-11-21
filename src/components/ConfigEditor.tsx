import React, { ChangeEvent } from 'react';
import {
  DataSourcePluginOptionsEditorProps,
  onUpdateDatasourceJsonDataOption,
  onUpdateDatasourceSecureJsonDataOption,
  updateDatasourcePluginResetOption,
} from '@grafana/data';
import { config } from '@grafana/runtime';
import {
  Alert,
  Field,
  Input,
  RadioButtonGroup,
  SecretInput,
  SecretTextArea,
  SecureSocksProxySettings,
  Stack,
  Switch,
  Text,
} from '@grafana/ui';
import { CLOUD_REGIONS, Deployment, deploymentOf } from '../deployment';
import { SonarOptions, SonarSecureJsonData } from '../types';

type Props = DataSourcePluginOptionsEditorProps<SonarOptions, SonarSecureJsonData>;

const deploymentOptions: Array<{ label: string; value: Deployment; description: string }> = [
  { label: 'SonarQube Server', value: 'server', description: 'A SonarQube you host yourself (Community Build or a commercial edition)' },
  { label: 'SonarQube Cloud', value: 'cloud-eu', description: 'sonarcloud.io' },
  { label: 'SonarQube Cloud (US)', value: 'cloud-us', description: 'sonarqube.us' },
];

const WIDTH = 50;

export function ConfigEditor(props: Props) {
  const { options, onOptionsChange } = props;
  const { jsonData, secureJsonFields } = options;
  const deployment = deploymentOf(options.url);
  const isCloud = deployment !== 'server';
  const plainHttp = options.url?.trim().toLowerCase().startsWith('http://');
  const tokenSet = !!secureJsonFields?.token || !!options.secureJsonData?.token;

  const setJson = <K extends keyof SonarOptions>(key: K, value: SonarOptions[K]) =>
    onOptionsChange({ ...options, jsonData: { ...jsonData, [key]: value } });

  const onDeployment = (next: Deployment) => {
    if (next === deployment) {
      return;
    }
    // The backend tells Cloud from Server by the URL, so choosing Cloud sets
    // it, and going back to Server clears a Cloud URL.
    onOptionsChange({ ...options, url: next === 'server' ? '' : CLOUD_REGIONS[next] });
  };

  const onTimeout = (e: ChangeEvent<HTMLInputElement>) => {
    const n = parseInt(e.currentTarget.value, 10);
    setJson('timeoutSeconds', Number.isFinite(n) ? n : undefined);
  };

  return (
    <Stack direction="column" gap={3}>
      <section>
        <Text element="h3">Connection</Text>
        <Field label="SonarQube">
          <RadioButtonGroup id="sonar-deployment" options={deploymentOptions} value={deployment} onChange={onDeployment} />
        </Field>

        {isCloud ? (
          <>
            <Field label="URL">
              <Input id="sonar-url" width={WIDTH} value={options.url || ''} disabled />
            </Field>
            <Field
              label="Organization key"
              description="The last part of your organization's address, for example my-org in sonarcloud.io/organizations/my-org."
              required
            >
              <Input
                id="sonar-organization"
                width={WIDTH}
                placeholder="my-org"
                value={jsonData.organization || ''}
                onChange={onUpdateDatasourceJsonDataOption(props, 'organization')}
              />
            </Field>
          </>
        ) : (
          <Field
            label="URL"
            description="The address you open SonarQube at, including any path prefix, for example https://sonarqube.example.com or https://tools.example.com/sonarqube."
            required
          >
            <Input
              id="sonar-url"
              width={WIDTH}
              value={options.url || ''}
              placeholder="https://sonarqube.example.com"
              onChange={(e) => onOptionsChange({ ...options, url: e.currentTarget.value })}
            />
          </Field>
        )}
      </section>

      <section>
        <Text element="h3">Authentication</Text>
        <Field
          label="Token"
          description={
            <>
              A user token: in SonarQube, open My Account &gt; Security and generate a token of type User. The plugin only
              reads, so a user with Browse permission on your projects is enough.{' '}
              {isCloud
                ? 'Leave empty to read only the public projects of the organization.'
                : 'Leave empty only if your SonarQube allows anonymous access.'}
            </>
          }
        >
          <SecretInput
            id="sonar-token"
            width={WIDTH}
            autoComplete="new-password"
            placeholder={isCloud ? 'Token (optional for public projects)' : 'squ_…'}
            isConfigured={!!secureJsonFields?.token}
            value={options.secureJsonData?.token || ''}
            onChange={onUpdateDatasourceSecureJsonDataOption(props, 'token')}
            onReset={() => updateDatasourcePluginResetOption(props, 'token')}
          />
        </Field>
        {plainHttp && tokenSet && (
          <Alert severity="warning" title="Token over plain HTTP">
            The URL uses http://, so the token is sent unencrypted. Use https:// unless Grafana and SonarQube share a
            trusted network.
          </Alert>
        )}
      </section>

      {!isCloud && config.secureSocksDSProxyEnabled && (
        <section>
          <Text element="h3">Private network</Text>
          <SecureSocksProxySettings options={options} onOptionsChange={onOptionsChange} />
        </section>
      )}

      {!isCloud && (
        <section>
          <Text element="h3">TLS</Text>
          <Field label="Custom CA certificate" description="Verify SonarQube's certificate against this CA (PEM), for a private or internal CA.">
            <Switch
              id="sonar-tls-with-ca"
              value={!!jsonData.tlsAuthWithCACert}
              onChange={(e) => setJson('tlsAuthWithCACert', e.currentTarget.checked)}
            />
          </Field>
          {jsonData.tlsAuthWithCACert && (
            <Field label="CA certificate">
              <SecretTextArea
                id="sonar-tls-ca"
                cols={60}
                rows={6}
                placeholder="-----BEGIN CERTIFICATE-----"
                isConfigured={!!secureJsonFields?.tlsCACert}
                onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsCACert')}
                onReset={() => updateDatasourcePluginResetOption(props, 'tlsCACert')}
              />
            </Field>
          )}
          <Field label="Client certificate" description="Present a client certificate to SonarQube (mutual TLS), if a proxy in front of it requires one.">
            <Switch
              id="sonar-tls-client-auth"
              value={!!jsonData.tlsAuth}
              onChange={(e) => setJson('tlsAuth', e.currentTarget.checked)}
            />
          </Field>
          {jsonData.tlsAuth && (
            <>
              <Field label="Server name" description="Expected name in the server's certificate, if it differs from the URL host.">
                <Input
                  id="sonar-tls-server-name"
                  width={WIDTH}
                  value={jsonData.serverName || ''}
                  onChange={onUpdateDatasourceJsonDataOption(props, 'serverName')}
                />
              </Field>
              <Field label="Client certificate (PEM)">
                <SecretTextArea
                  id="sonar-tls-client-cert"
                  cols={60}
                  rows={6}
                  placeholder="-----BEGIN CERTIFICATE-----"
                  isConfigured={!!secureJsonFields?.tlsClientCert}
                  onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientCert')}
                  onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientCert')}
                />
              </Field>
              <Field label="Client key (PEM)">
                <SecretTextArea
                  id="sonar-tls-client-key"
                  cols={60}
                  rows={6}
                  placeholder="-----BEGIN PRIVATE KEY-----"
                  isConfigured={!!secureJsonFields?.tlsClientKey}
                  onChange={onUpdateDatasourceSecureJsonDataOption(props, 'tlsClientKey')}
                  onReset={() => updateDatasourcePluginResetOption(props, 'tlsClientKey')}
                />
              </Field>
            </>
          )}
          <Field label="Skip TLS certificate verification" description="Insecure. Only for testing.">
            <Switch
              id="sonar-tls-skip-verify"
              value={!!jsonData.tlsSkipVerify}
              onChange={(e) => setJson('tlsSkipVerify', e.currentTarget.checked)}
            />
          </Field>
        </section>
      )}

      <section>
        <Text element="h3">Advanced</Text>
        <Field
          label="Query timeout (seconds)"
          description="How long one panel query may take. A query over many projects makes several requests. Default 30."
        >
          <Input
            id="sonar-timeout"
            type="number"
            min={1}
            max={300}
            width={20}
            placeholder="30"
            value={jsonData.timeoutSeconds ?? ''}
            onChange={onTimeout}
          />
        </Field>
      </section>
    </Stack>
  );
}
