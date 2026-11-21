import React, { useEffect, useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { Combobox, ComboboxOption, InlineField, Stack, Text } from '@grafana/ui';
import { DataSource } from '../datasource';
import { SonarOptions, SonarQuery, SonarVariableQuery } from '../types';

type Props = QueryEditorProps<DataSource, SonarQuery, SonarOptions, SonarVariableQuery>;

const listOptions: Array<ComboboxOption<NonNullable<SonarVariableQuery['list']>>> = [
  { value: 'projects', label: 'Projects', description: 'Shows project names; the value is the project key' },
  { value: 'metrics', label: 'Metrics', description: 'Shows metric names; the value is the metric key' },
  { value: 'branches', label: 'Branches of a project', description: 'The branch names of one project' },
];

/** The editor for a dashboard variable of type Query. */
export function VariableQueryEditor({ query, onChange, datasource }: Props) {
  const list = query.list ?? 'projects';
  const [projects, setProjects] = useState<Array<ComboboxOption<string>>>([]);
  useEffect(() => {
    let live = true;
    datasource.getProjects().then(
      (p) => live && setProjects(p.projects.map((x) => ({ value: x.key, label: x.name, description: x.key }))),
      () => live && setProjects([])
    );
    return () => {
      live = false;
    };
  }, [datasource]);

  const set = (patch: Partial<SonarVariableQuery>) => onChange({ ...query, refId: query.refId || 'variable', ...patch });

  return (
    <Stack direction="column" gap={1}>
      <InlineField label="List" labelWidth={14}>
        <Combobox id="sonar-variable-list" width={32} options={listOptions} value={list} onChange={(o) => set({ list: o.value })} />
      </InlineField>
      {list === 'branches' && (
        <InlineField label="Project" labelWidth={14} tooltip="A project, a project key, or another variable such as $project.">
          <Combobox
            id="sonar-variable-project"
            width={40}
            createCustomValue
            placeholder="Choose a project"
            options={projects}
            value={query.project ?? null}
            onChange={(o) => set({ project: o?.value ?? '' })}
          />
        </InlineField>
      )}
      <Text variant="bodySmall" color="secondary">
        Use the variable in a query by choosing it in the Projects or Metrics field. A multi-value variable selects
        several at once.
      </Text>
    </Stack>
  );
}
