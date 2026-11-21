import React, { useEffect, useMemo, useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { getTemplateSrv } from '@grafana/runtime';
import {
  Alert,
  Combobox,
  ComboboxOption,
  InlineField,
  InlineSwitch,
  Input,
  MultiCombobox,
  RadioButtonGroup,
  Stack,
  Text,
} from '@grafana/ui';
import { DataSource, ProjectList } from '../datasource';
import {
  COMMON_METRICS,
  defaultsFor,
  EVENT_CATEGORIES,
  GROUP_BYS,
  ISSUE_TYPES,
  missing,
  QUERY_TYPES,
  queryTypeInfo,
  resolveKeys,
  SEVERITIES,
} from '../query';
import { MetricInfo, QueryType, Resolution, SonarOptions, SonarQuery } from '../types';

type Props = QueryEditorProps<DataSource, SonarQuery, SonarOptions>;
type Option = ComboboxOption<string>;

const LABEL_WIDTH = 14;

const resolutionOptions: Array<{ label: string; value: Resolution }> = [
  { label: 'Open', value: 'open' },
  { label: 'Resolved', value: 'resolved' },
  { label: 'All', value: 'all' },
];

const queryTypeOptions: Option[] = QUERY_TYPES.map((t) => ({ value: t.value, label: t.label, description: t.description }));

/** Loads something once per data source and reports a failure as text. */
function useLoaded<T>(load: () => Promise<T>, datasource: DataSource): { data?: T; error?: string } {
  const [state, setState] = useState<{ data?: T; error?: string }>({});
  useEffect(() => {
    let live = true;
    load().then(
      (data) => live && setState({ data }),
      (err) => live && setState({ error: err?.data?.message ?? err?.message ?? 'The request failed.' })
    );
    return () => {
      live = false;
    };
    // `load` is a method of `datasource`; reload only when that changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasource]);
  return state;
}

/** The selected values as options, keeping ones that are not in the list (typed keys, variables). */
function selected(values: string[] | undefined, options: Option[]): Option[] {
  return (values ?? []).map((v) => options.find((o) => o.value === v) ?? { value: v, label: v });
}

function metricOptions(metrics: MetricInfo[]): Option[] {
  const byKey = new Map(metrics.map((m) => [m.key, m]));
  const option = (m: MetricInfo, group: string): Option => ({ value: m.key, label: m.name, description: m.key, group });
  const common = COMMON_METRICS.flatMap((k) => (byKey.has(k) ? [option(byKey.get(k)!, 'Common')] : []));
  const rest = metrics
    .filter((m) => !COMMON_METRICS.includes(m.key))
    .sort((a, b) => (a.domain ?? '').localeCompare(b.domain ?? '') || a.name.localeCompare(b.name))
    .map((m) => option(m, m.domain || 'Other'));
  return [...common, ...rest];
}

export function QueryEditor({ query, onChange, onRunQuery, datasource, data }: Props) {
  const info = queryTypeInfo(query.queryType);
  const projects = useLoaded<ProjectList>(() => datasource.getProjects(), datasource);
  const metrics = useLoaded<MetricInfo[]>(() => datasource.getMetrics(), datasource);

  // Dashboard variables can be chosen wherever a project or metric can.
  const variableOptions = useMemo<Option[]>(
    () =>
      getTemplateSrv()
        .getVariables()
        .map((v) => ({ value: `$${v.name}`, label: `$${v.name}`, description: 'Dashboard variable', group: 'Variables' })),
    []
  );
  const projectOpts = useMemo<Option[]>(
    () => [
      ...variableOptions,
      ...(projects.data?.projects ?? []).map((p) => ({ value: p.key, label: p.name, description: p.key, group: 'Projects' })),
    ],
    [projects.data, variableOptions]
  );
  const metricOpts = useMemo<Option[]>(
    () => [...variableOptions, ...metricOptions(metrics.data ?? [])],
    [metrics.data, variableOptions]
  );

  const [branch, setBranch] = useState({ base: query.branch ?? '', text: query.branch ?? '' });
  const branchText = branch.base === (query.branch ?? '') ? branch.text : (query.branch ?? '');
  const [limit, setLimit] = useState<string | null>(null);

  // A query that has never been edited has no type yet: give it the defaults
  // (Grafana applies getDefaultQuery in some places only).
  useEffect(() => {
    if (!query.queryType) {
      onChange(defaultsFor('measureHistory', query));
    }
    // Once, when the editor opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const update = (patch: Partial<SonarQuery>) => {
    onChange({ ...query, ...patch });
    onRunQuery();
  };
  const values = (opts: Option[]) => opts.map((o) => o.value);
  // Typing a name and pressing Enter offers it as a custom value first; when
  // it is the name of a listed project or metric, use that one's key.
  const keys = (opts: Option[], known: Option[]) => resolveKeys(values(opts), known);

  const hint = missing(query);
  // What the backend said about an empty result, for example that the time
  // range holds no analysis.
  const notice = (data?.series ?? [])
    .filter((frame) => frame.refId === query.refId)
    .flatMap((frame) => frame.meta?.notices ?? [])
    .map((n) => n.text)[0];
  const loadError = projects.error ?? metrics.error;
  const listed = projects.data?.projects.length ?? 0;
  const total = projects.data?.total ?? 0;

  return (
    <Stack direction="column" gap={1}>
      <InlineField label="Query" labelWidth={LABEL_WIDTH} tooltip={info.description}>
        <Combobox
          id="sonar-query-type"
          width={32}
          options={queryTypeOptions}
          value={info.value}
          onChange={(o) => {
            onChange(defaultsFor(o.value as QueryType, query));
            onRunQuery();
          }}
        />
      </InlineField>

      {info.projects !== 'none' && (
        <InlineField
          label="Projects"
          labelWidth={LABEL_WIDTH}
          grow
          tooltip={
            info.projects === 'optional'
              ? 'Leave empty for every project the token can see. You can also type a project key or choose a dashboard variable.'
              : 'Choose projects, type a project key, or choose a dashboard variable.'
          }
        >
          <MultiCombobox
            id="sonar-projects"
            width="auto"
            minWidth={40}
            maxWidth={100}
            isClearable
            createCustomValue
            placeholder={info.projects === 'optional' ? 'All projects' : 'Choose projects'}
            options={projectOpts}
            value={selected(query.projects, projectOpts)}
            onChange={(opts) => update({ projects: keys(opts, projectOpts) })}
          />
        </InlineField>
      )}

      {info.metrics && (
        <InlineField label="Metrics" labelWidth={LABEL_WIDTH} grow tooltip="What to measure. Type to search by name or key.">
          <MultiCombobox
            id="sonar-metrics"
            width="auto"
            minWidth={40}
            maxWidth={100}
            isClearable
            createCustomValue
            placeholder="Choose metrics"
            options={metricOpts}
            value={selected(query.metrics, metricOpts)}
            onChange={(opts) => update({ metrics: keys(opts, metricOpts) })}
          />
        </InlineField>
      )}

      {info.issueFilters && (
        <>
          <Stack direction="row" gap={1} wrap="wrap">
            <InlineField label="Status" labelWidth={LABEL_WIDTH}>
              <RadioButtonGroup
                id="sonar-resolution"
                options={resolutionOptions}
                value={query.resolution ?? 'open'}
                onChange={(resolution) => update({ resolution })}
              />
            </InlineField>
            <InlineField label="Severity" labelWidth={10} tooltip="Leave empty for every severity.">
              <MultiCombobox
                id="sonar-severities"
                width="auto"
                minWidth={20}
                isClearable
                placeholder="Any"
                options={SEVERITIES}
                value={query.severities ?? []}
                onChange={(opts) => update({ severities: values(opts) })}
              />
            </InlineField>
            <InlineField label="Type" labelWidth={8} tooltip="Leave empty for every type.">
              <MultiCombobox
                id="sonar-types"
                width="auto"
                minWidth={20}
                isClearable
                placeholder="Any"
                options={ISSUE_TYPES}
                value={query.types ?? []}
                onChange={(opts) => update({ types: values(opts) })}
              />
            </InlineField>
          </Stack>
          <Stack direction="row" gap={1} wrap="wrap">
            {info.value === 'issueCounts' ? (
              <InlineField label="Group by" labelWidth={LABEL_WIDTH} tooltip="Count issues per value of this property.">
                <Combobox
                  id="sonar-group-by"
                  width={24}
                  options={GROUP_BYS}
                  value={query.groupBy ?? 'severities'}
                  onChange={(o) => update({ groupBy: o.value })}
                />
              </InlineField>
            ) : (
              <InlineField
                label="Limit"
                labelWidth={LABEL_WIDTH}
                tooltip="The newest issues are returned first. SonarQube returns at most 10,000 issues per search."
              >
                <Input
                  id="sonar-limit"
                  type="number"
                  min={1}
                  max={10000}
                  width={14}
                  value={limit ?? String(query.limit ?? 100)}
                  onChange={(e) => setLimit(e.currentTarget.value)}
                  onBlur={() => {
                    const n = Math.min(10000, Math.max(1, parseInt(limit ?? '', 10) || query.limit || 100));
                    setLimit(null);
                    if (n !== query.limit) {
                      update({ limit: n });
                    }
                  }}
                />
              </InlineField>
            )}
            <InlineField
              label="Created in time range"
              labelWidth={24}
              tooltip="Only issues created inside the dashboard's time range. Off: issues of any age."
            >
              <InlineSwitch
                id="sonar-created-in-range"
                value={!!query.createdInRange}
                onChange={(e) => update({ createdInRange: e.currentTarget.checked })}
              />
            </InlineField>
          </Stack>
        </>
      )}

      {info.value === 'analyses' && (
        <InlineField
          label="Events"
          labelWidth={LABEL_WIDTH}
          grow
          tooltip="Only analyses that have one of these events. Leave empty for every analysis."
        >
          <MultiCombobox
            id="sonar-categories"
            width="auto"
            minWidth={40}
            isClearable
            placeholder="Every analysis"
            options={EVENT_CATEGORIES}
            value={query.categories ?? []}
            onChange={(opts) => update({ categories: values(opts) })}
          />
        </InlineField>
      )}

      {info.branch && (
        <InlineField
          label="Branch"
          labelWidth={LABEL_WIDTH}
          tooltip="Leave empty for the main branch. Branch analysis needs SonarQube Developer Edition or higher, or SonarQube Cloud."
        >
          <Input
            id="sonar-branch"
            width={32}
            placeholder="Main branch"
            value={branchText}
            onChange={(e) => setBranch({ base: query.branch ?? '', text: e.currentTarget.value })}
            onBlur={() => {
              if (branchText.trim() !== (query.branch ?? '')) {
                update({ branch: branchText.trim() });
              }
            }}
          />
        </InlineField>
      )}

      {hint && (
        <Text color="info" data-testid="sonar-query-hint">
          {hint}
        </Text>
      )}
      {!hint && notice && (
        <Text color="info" data-testid="sonar-query-notice">
          {notice}
        </Text>
      )}
      {total > listed && info.projects !== 'none' && (
        <Text variant="bodySmall" color="secondary">
          The list shows the first {listed} of {total} projects. Type a project key to use one that is not listed.
        </Text>
      )}
      {loadError && (
        <Alert severity="error" title="Could not load the lists from SonarQube" data-testid="sonar-load-error">
          {loadError} Check the data source settings with Save &amp; test.
        </Alert>
      )}
    </Stack>
  );
}
