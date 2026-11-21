import { CLOUD_REGIONS, deploymentOf } from './deployment';
import {
  COMMON_METRICS,
  DEFAULT_QUERY,
  defaultsFor,
  expandList,
  GROUP_BYS,
  isVariable,
  missing,
  QUERY_TYPES,
  queryTypeInfo,
  resolveKeys,
  variableToQuery,
} from './query';
import { SonarQuery } from './types';

const q = (patch: Partial<SonarQuery>): SonarQuery => ({ refId: 'A', ...patch });

describe('query types', () => {
  it('offers the six panel query types, history first', () => {
    expect(QUERY_TYPES.map((t) => t.value)).toEqual([
      'measureHistory',
      'measures',
      'issues',
      'issueCounts',
      'analyses',
      'projects',
    ]);
    expect(QUERY_TYPES.every((t) => t.label && t.description)).toBe(true);
  });

  it('falls back to metric history for a missing or unknown type', () => {
    expect(queryTypeInfo(undefined).value).toBe('measureHistory');
    expect(queryTypeInfo('nonsense' as never).value).toBe('measureHistory');
  });

  it('starts new queries as coverage history', () => {
    expect(DEFAULT_QUERY).toEqual({ queryType: 'measureHistory', projects: [], metrics: ['coverage'] });
    expect(COMMON_METRICS).toContain('coverage');
  });

  // The backend validates against the same list (pkg/models/query.go).
  it('groups issues by the properties the backend accepts', () => {
    expect(GROUP_BYS.map((g) => g.value)).toEqual([
      'severities',
      'types',
      'projects',
      'rules',
      'tags',
      'languages',
      'assignees',
      'author',
      'resolutions',
      'statuses',
    ]);
  });
});

describe('defaultsFor', () => {
  it('fills what a type needs when switching to it', () => {
    expect(defaultsFor('measures', q({ projects: ['p'] }))).toMatchObject({
      queryType: 'measures',
      projects: ['p'],
      metrics: ['alert_status', 'coverage', 'bugs', 'vulnerabilities', 'code_smells'],
    });
    expect(defaultsFor('issues', q({}))).toMatchObject({ queryType: 'issues', resolution: 'open', limit: 100 });
    expect(defaultsFor('issueCounts', q({}))).toMatchObject({ queryType: 'issueCounts', resolution: 'open', groupBy: 'severities' });
    expect(defaultsFor('analyses', q({}))).toMatchObject({ queryType: 'analyses', categories: ['VERSION', 'QUALITY_GATE'] });
  });

  it('gives a fresh query the table metrics when it becomes a table', () => {
    expect(defaultsFor('measures', q({ queryType: 'measureHistory', metrics: ['coverage'] })).metrics).toEqual([
      'alert_status',
      'coverage',
      'bugs',
      'vulnerabilities',
      'code_smells',
    ]);
    expect(defaultsFor('measureHistory', q({ queryType: 'issues' })).metrics).toEqual(['coverage']);
  });

  it('keeps what the user already chose', () => {
    const next = defaultsFor('measures', q({ queryType: 'measureHistory', projects: ['a'], metrics: ['bugs'], branch: 'main' }));
    expect(next).toMatchObject({ queryType: 'measures', projects: ['a'], metrics: ['bugs'], branch: 'main' });
    expect(defaultsFor('issues', q({ resolution: 'all', limit: 5 }))).toMatchObject({ resolution: 'all', limit: 5 });
    // An explicitly emptied event filter stays empty.
    expect(defaultsFor('analyses', q({ categories: [] })).categories).toEqual([]);
  });
});

describe('missing', () => {
  it('says what a query still needs', () => {
    expect(missing(q({ queryType: 'measureHistory', projects: [], metrics: ['coverage'] }))).toBe('Choose at least one project.');
    expect(missing(q({ queryType: 'measureHistory', projects: ['p'], metrics: [] }))).toBe('Choose at least one metric.');
    expect(missing(q({ queryType: 'measures', metrics: [] }))).toBe('Choose at least one metric.');
    expect(missing(q({ queryType: 'analyses' }))).toBe('Choose at least one project.');
  });

  it('is null when the query can run', () => {
    expect(missing(q({ queryType: 'measureHistory', projects: ['p'], metrics: ['coverage'] }))).toBeNull();
    expect(missing(q({ queryType: 'measures', metrics: ['coverage'] }))).toBeNull();
    expect(missing(q({ queryType: 'issues' }))).toBeNull();
    expect(missing(q({ queryType: 'issueCounts' }))).toBeNull();
    expect(missing(q({ queryType: 'projects' }))).toBeNull();
    expect(missing(q({ queryType: 'analyses', projects: ['$project'] }))).toBeNull();
  });
});

describe('expandList', () => {
  const vars: Record<string, string> = { $project: 'a:one,b.two', '${metric}': 'coverage', $empty: '' };
  const replace = (v: string) => vars[v] ?? v;

  it('expands a multi-value variable into its values', () => {
    expect(expandList(['$project'], replace)).toEqual(['a:one', 'b.two']);
  });

  it('mixes variables and fixed values, without duplicates', () => {
    expect(expandList(['b.two', '$project', 'c'], replace)).toEqual(['b.two', 'a:one', 'c']);
    expect(expandList(['${metric}', 'coverage', ' bugs '], replace)).toEqual(['coverage', 'bugs']);
  });

  it('drops a variable with no value, and handles no list', () => {
    expect(expandList(['$empty'], replace)).toEqual([]);
    expect(expandList(undefined, replace)).toEqual([]);
  });

  it('leaves an unknown variable for the backend to report', () => {
    expect(expandList(['$nope'], replace)).toEqual(['$nope']);
  });
});

describe('isVariable', () => {
  it.each(['$project', '${project}', '${project:csv}', ' $p_1 '])('%s is a variable', (v) => expect(isVariable(v)).toBe(true));
  it.each(['demo:shop-api', 'cost$', '$', '$1x', 'a $b'])('%s is not', (v) => expect(isVariable(v)).toBe(false));
});

describe('resolveKeys', () => {
  const known = [
    { value: '$project', label: '$project' },
    { value: 'demo:shop-api', label: 'Shop API' },
    { value: 'demo:shop-web', label: 'Shop Web' },
    { value: 'a:dup', label: 'Twin' },
    { value: 'b:dup', label: 'Twin' },
  ];

  it('keeps keys and variables as they are', () => {
    expect(resolveKeys(['demo:shop-api', '$project', '${other}'], known)).toEqual(['demo:shop-api', '$project', '${other}']);
  });

  it('turns a typed name into its key, ignoring case and spaces', () => {
    expect(resolveKeys(['Shop API'], known)).toEqual(['demo:shop-api']);
    expect(resolveKeys([' shop web '], known)).toEqual(['demo:shop-web']);
  });

  it('keeps an unknown value as typed, for keys that are not listed', () => {
    expect(resolveKeys(['other:project'], known)).toEqual(['other:project']);
  });

  it('does not guess when two entries share the name', () => {
    expect(resolveKeys(['Twin'], known)).toEqual(['Twin']);
  });

  it('removes duplicates, including a name and its own key', () => {
    expect(resolveKeys(['demo:shop-api', 'Shop API', 'demo:shop-api'], known)).toEqual(['demo:shop-api']);
  });
});

describe('variableToQuery', () => {
  const replace = (v: string) => (v === '$project' ? 'demo:shop-api' : v);

  it('lists projects by default', () => {
    expect(variableToQuery({ refId: '' }, replace)).toEqual({ refId: 'variable', queryType: 'projects' });
    expect(variableToQuery({ refId: 'X', list: 'projects' }, replace)).toEqual({ refId: 'X', queryType: 'projects' });
  });

  it('lists metrics', () => {
    expect(variableToQuery({ refId: 'X', list: 'metrics' }, replace)).toEqual({ refId: 'X', queryType: 'metrics' });
  });

  it('lists the branches of a project, which may be another variable', () => {
    expect(variableToQuery({ refId: 'X', list: 'branches', project: '$project' }, replace)).toEqual({
      refId: 'X',
      queryType: 'branches',
      projects: ['demo:shop-api'],
    });
    // No project yet: the backend answers "choose at least one project".
    expect(variableToQuery({ refId: 'X', list: 'branches' }, replace).projects).toEqual([]);
  });
});

// Mirrors IsCloudHost in pkg/models/settings.go.
describe('deploymentOf', () => {
  it.each([
    ['https://sonarcloud.io', 'cloud-eu'],
    ['https://sonarcloud.io/', 'cloud-eu'],
    ['https://www.sonarcloud.io', 'cloud-eu'],
    ['https://SonarCloud.io', 'cloud-eu'],
    ['https://sonarqube.us', 'cloud-us'],
    ['https://sonarqube.example.com', 'server'],
    ['https://sonarcloud.io.evil.test', 'server'],
    ['https://notsonarcloud.io', 'server'],
    ['http://localhost:9000', 'server'],
    ['not a url', 'server'],
    ['', 'server'],
    [undefined, 'server'],
  ])('%s is %s', (url, want) => expect(deploymentOf(url)).toBe(want));

  it('round-trips the URLs the editor sets for Cloud', () => {
    expect(deploymentOf(CLOUD_REGIONS['cloud-eu'])).toBe('cloud-eu');
    expect(deploymentOf(CLOUD_REGIONS['cloud-us'])).toBe('cloud-us');
  });
});
