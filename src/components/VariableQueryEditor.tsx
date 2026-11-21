import React, { useEffect, useState } from 'react';
import { QueryEditorProps } from '@grafana/data';
import { Combobox, ComboboxOption, InlineField, MultiCombobox, Stack, Text } from '@grafana/ui';
import { DataSource } from '../datasource';
import { SonarOptions, SonarQuery, SonarVariableQuery } from '../types';

type Props = QueryEditorProps<DataSource, SonarQuery, SonarOptions, SonarVariableQuery>;

const listOptions: Array<ComboboxOption<NonNullable<SonarVariableQuery['list']>>> = [
  { value: 'projects', label: 'Projects', description: 'Shows project names; the value is the project key' },
  { value: 'metrics', label: 'Metrics', description: 'Shows metric names; the value is the metric key' },
  { value: 'branches', label: 'Branches of a project', description: 'The branch names of one project' },
  {
    value: 'pullRequests',
    label: 'Pull requests of a project',
    description: 'Shows each pull request number and title; the value is the number',
  },
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

  const [tags, setTags] = useState<Array<ComboboxOption<string>>>([]);
  useEffect(() => {
    let live = true;
    datasource.getTags().then(
      (t) => live && setTags(t.map((x) => ({ value: x, label: x }))),
      () => live && setTags([])
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
      {list === 'projects' && (
        <InlineField
          label="Tags"
          labelWidth={14}
          grow
          tooltip="Only projects that have one of these tags. Leave empty for every project. You can also type a tag, or another variable such as $team."
        >
          <MultiCombobox
            id="sonar-variable-tags"
            width="auto"
            minWidth={32}
            maxWidth={80}
            isClearable
            createCustomValue
            placeholder="Any tag"
            options={tags}
            value={(query.tags ?? []).map((t) => tags.find((o) => o.value === t) ?? { value: t, label: t })}
            onChange={(opts) => set({ tags: opts.map((o) => o.value.trim()).filter(Boolean) })}
          />
        </InlineField>
      )}
      {(list === 'branches' || list === 'pullRequests') && (
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
