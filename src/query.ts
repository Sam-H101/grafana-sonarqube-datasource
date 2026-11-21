/**
 * Query logic that needs no Grafana runtime, so it can be unit tested: what
 * each query type needs, defaults, and how dashboard variables expand.
 */
import { QueryType, SonarQuery, SonarVariableQuery } from './types';

export interface QueryTypeInfo {
  value: QueryType;
  label: string;
  description: string;
  /** Which editor fields the type uses. */
  projects: 'required' | 'optional' | 'none';
  metrics: boolean;
  branch: boolean;
  /** Whether the type can look at a pull request instead of a branch. */
  pullRequest: boolean;
  issueFilters: boolean;
}

/** The query types offered in the panel query editor, in menu order. */
export const QUERY_TYPES: QueryTypeInfo[] = [
  {
    value: 'measureHistory',
    label: 'Metric history',
    description: 'Metric values over time, for example coverage or bugs per project',
    projects: 'required',
    metrics: true,
    branch: true,
    pullRequest: true,
    issueFilters: false,
  },
  {
    value: 'measures',
    label: 'Current metrics',
    description: 'The latest value of each metric, one row per project',
    projects: 'optional',
    metrics: true,
    branch: true,
    pullRequest: true,
    issueFilters: false,
  },
  {
    value: 'qualityGate',
    label: 'Quality gate conditions',
    description: 'Each condition of the quality gate with its value and what the gate requires: why a gate failed',
    projects: 'optional',
    metrics: false,
    branch: true,
    pullRequest: true,
    issueFilters: false,
  },
  {
    value: 'issues',
    label: 'Issues',
    description: 'A list of issues, newest first',
    projects: 'optional',
    metrics: false,
    branch: true,
    pullRequest: true,
    issueFilters: true,
  },
  {
    value: 'issueCounts',
    label: 'Issue counts',
    description: 'How many issues there are per severity, type, project, rule and so on',
    projects: 'optional',
    metrics: false,
    branch: true,
    pullRequest: true,
    issueFilters: true,
  },
  {
    value: 'hotspots',
    label: 'Security hotspots',
    description: 'A list of security hotspots, highest review priority first',
    projects: 'required',
    metrics: false,
    branch: true,
    pullRequest: true,
    issueFilters: false,
  },
  {
    value: 'analyses',
    label: 'Analyses and events',
    description: 'Analyses with their events: new versions and quality gate changes. Good for annotations',
    projects: 'required',
    metrics: false,
    branch: true,
    pullRequest: false,
    issueFilters: false,
  },
  {
    value: 'pullRequests',
    label: 'Pull requests',
    description: 'The pull requests SonarQube has analysed, with their quality gate',
    projects: 'required',
    metrics: false,
    branch: false,
    pullRequest: false,
    issueFilters: false,
  },
  {
    value: 'projects',
    label: 'Projects',
    description: 'The projects the token can see, or those with a tag',
    projects: 'none',
    metrics: false,
    branch: false,
    pullRequest: false,
    issueFilters: false,
  },
];

export function queryTypeInfo(type?: QueryType): QueryTypeInfo {
  return QUERY_TYPES.find((t) => t.value === type) ?? QUERY_TYPES[0];
}

export const SEVERITIES = [
  { value: 'BLOCKER', label: 'Blocker' },
  { value: 'CRITICAL', label: 'Critical' },
  { value: 'MAJOR', label: 'Major' },
  { value: 'MINOR', label: 'Minor' },
  { value: 'INFO', label: 'Info' },
];

export const ISSUE_TYPES = [
  { value: 'BUG', label: 'Bug' },
  { value: 'VULNERABILITY', label: 'Vulnerability' },
  { value: 'CODE_SMELL', label: 'Code smell' },
];

export const GROUP_BYS = [
  { value: 'severities', label: 'Severity' },
  { value: 'types', label: 'Type' },
  { value: 'projects', label: 'Project' },
  { value: 'rules', label: 'Rule' },
  { value: 'tags', label: 'Tag' },
  { value: 'languages', label: 'Language' },
  { value: 'assignees', label: 'Assignee' },
  { value: 'author', label: 'Author' },
  { value: 'resolutions', label: 'Resolution' },
  { value: 'statuses', label: 'Status' },
];

export const HOTSPOT_STATUSES = [
  { value: 'toReview', label: 'To review' },
  { value: 'reviewed', label: 'Reviewed' },
  { value: 'all', label: 'All' },
] as const;

export const EVENT_CATEGORIES = [
  { value: 'VERSION', label: 'New version' },
  { value: 'QUALITY_GATE', label: 'Quality gate change' },
  { value: 'QUALITY_PROFILE', label: 'Quality profile change' },
  { value: 'DEFINITION_CHANGE', label: 'Definition change' },
  { value: 'OTHER', label: 'Other' },
];

/** Metrics offered first in the metric dropdown, and used for new queries. */
export const COMMON_METRICS = [
  'coverage',
  'bugs',
  'vulnerabilities',
  'code_smells',
  'security_hotspots',
  'duplicated_lines_density',
  'ncloc',
  'sqale_index',
  'alert_status',
  'reliability_rating',
  'security_rating',
  'sqale_rating',
];

export const DEFAULT_QUERY: Partial<SonarQuery> = {
  queryType: 'measureHistory',
  projects: [],
  metrics: ['coverage'],
};

/** Fields a query type needs when the user switches to it. */
export function defaultsFor(type: QueryType, query: SonarQuery): SonarQuery {
  const next: SonarQuery = { ...query, queryType: type };
  const info = queryTypeInfo(type);
  // A query still on the untouched history default gets the table's own
  // default set, which is what a project overview usually shows.
  const untouched = !next.metrics?.length || (next.metrics.length === 1 && next.metrics[0] === 'coverage');
  if (type === 'measures' && untouched) {
    next.metrics = ['alert_status', 'coverage', 'bugs', 'vulnerabilities', 'code_smells'];
  } else if (info.metrics && !next.metrics?.length) {
    next.metrics = ['coverage'];
  }
  if (info.issueFilters) {
    next.resolution = next.resolution ?? 'open';
    if (type === 'issueCounts') {
      next.groupBy = next.groupBy ?? 'severities';
    } else {
      next.limit = next.limit ?? 100;
    }
  }
  if (type === 'hotspots') {
    next.hotspotStatus = next.hotspotStatus ?? 'toReview';
    next.limit = next.limit ?? 100;
  }
  // A branch or pull request does not carry over to a type that has no such
  // field, where it could not be seen or cleared.
  if (!info.branch) {
    delete next.branch;
  }
  if (!info.pullRequest) {
    delete next.pullRequest;
  }
  if (type === 'analyses' && !next.categories) {
    next.categories = ['VERSION', 'QUALITY_GATE'];
  }
  return next;
}

/** What is still missing before the query can run, or null if it is complete. */
export function missing(query: SonarQuery): string | null {
  const info = queryTypeInfo(query.queryType);
  if (info.projects === 'required' && !query.projects?.length) {
    return 'Choose at least one project.';
  }
  if (info.metrics && !query.metrics?.length) {
    return 'Choose at least one metric.';
  }
  if (info.pullRequest && query.branch?.trim() && query.pullRequest?.trim()) {
    return 'Choose a branch or a pull request, not both.';
  }
  return null;
}

/** True when the entry is a dashboard variable reference such as `$project` or `${project}`. */
export function isVariable(value: string): boolean {
  return /^\$(\{[^}]+\}|[A-Za-z_]\w*)$/.test(value.trim());
}

/**
 * Expands a list whose entries may be dashboard variables. `replace` turns
 * one entry into its value: a multi-value variable becomes a comma-separated
 * list, which is split here. Project keys, metric keys and the other values
 * used in these lists cannot contain commas.
 */
export function expandList(list: string[] | undefined, replace: (value: string) => string): string[] {
  const out: string[] = [];
  for (const entry of list ?? []) {
    for (const part of replace(entry).split(',')) {
      const value = part.trim();
      if (value && !out.includes(value)) {
        out.push(value);
      }
    }
  }
  return out;
}

/** The backend query a dashboard variable query runs. */
export function variableToQuery(variable: SonarVariableQuery, replace: (v: string) => string): SonarQuery {
  const refId = variable.refId || 'variable';
  switch (variable.list) {
    case 'metrics':
      return { refId, queryType: 'metrics' };
    case 'branches':
      return { refId, queryType: 'branches', projects: variable.project ? [replace(variable.project)] : [] };
    case 'pullRequests':
      return { refId, queryType: 'pullRequests', projects: variable.project ? [replace(variable.project)] : [] };
    default: {
      const tags = expandList(variable.tags, replace);
      return tags.length ? { refId, queryType: 'projects', tags } : { refId, queryType: 'projects' };
    }
  }
}

/**
 * Turns what was chosen or typed in a project or metric dropdown into keys.
 * A typed value that is not a key but is the name of exactly one known entry
 * (ignoring case) becomes that entry's key; anything else is kept as typed,
 * so keys that are not in the list and dashboard variables still work.
 */
export function resolveKeys(chosen: string[], known: Array<{ value: string; label?: string }>): string[] {
  const out: string[] = [];
  for (const raw of chosen) {
    const typed = raw.trim();
    let key = typed;
    if (!known.some((o) => o.value === typed) && !isVariable(typed)) {
      const byName = known.filter((o) => (o.label ?? '').toLowerCase() === typed.toLowerCase());
      if (byName.length === 1) {
        key = byName[0].value;
      }
    }
    if (key && !out.includes(key)) {
      out.push(key);
    }
  }
  return out;
}
