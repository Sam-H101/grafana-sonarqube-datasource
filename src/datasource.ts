import { AnnotationSupport, CoreApp, DataSourceInstanceSettings, ScopedVars } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { DEFAULT_QUERY, expandList, missing } from './query';
import { BranchInfo, MetricInfo, ProjectInfo, SonarOptions, SonarQuery } from './types';
import { SonarVariableSupport } from './variables';

export interface ProjectList {
  projects: ProjectInfo[];
  /** How many projects the token can see; may exceed projects.length. */
  total: number;
}

export class DataSource extends DataSourceWithBackend<SonarQuery, SonarOptions> {
  private projectList?: Promise<ProjectList>;
  private metricList?: Promise<MetricInfo[]>;
  private tagList?: Promise<string[]>;

  constructor(instanceSettings: DataSourceInstanceSettings<SonarOptions>) {
    super(instanceSettings);
    this.variables = new SonarVariableSupport(this);
    // Standard annotation support: the query editor is reused, and the
    // result's time, text and tags fields become the annotation.
    const annotations: AnnotationSupport<SonarQuery> = {
      getDefaultQuery: () => ({ queryType: 'analyses', projects: [], categories: ['VERSION', 'QUALITY_GATE'] }),
    };
    this.annotations = annotations;
  }

  getDefaultQuery(_: CoreApp): Partial<SonarQuery> {
    return DEFAULT_QUERY;
  }

  /** Incomplete queries are not sent: the editor says what is missing. */
  filterQuery(query: SonarQuery): boolean {
    return !query.hide && missing(query) === null;
  }

  applyTemplateVariables(query: SonarQuery, scopedVars: ScopedVars): SonarQuery {
    const templateSrv = getTemplateSrv();
    const list = (values?: string[]) => expandList(values, (v) => templateSrv.replace(v, scopedVars, 'csv'));
    return {
      ...query,
      projects: list(query.projects),
      metrics: list(query.metrics),
      severities: list(query.severities),
      types: list(query.types),
      categories: list(query.categories),
      tags: list(query.tags),
      branch: query.branch ? templateSrv.replace(query.branch, scopedVars) : query.branch,
      pullRequest: query.pullRequest ? templateSrv.replace(query.pullRequest, scopedVars) : query.pullRequest,
    };
  }

  /** Projects for the editor's dropdown, fetched once per data source instance. */
  getProjects(): Promise<ProjectList> {
    if (!this.projectList) {
      this.projectList = this.getResource<ProjectList>('projects').catch((err) => {
        this.projectList = undefined;
        throw err;
      });
    }
    return this.projectList;
  }

  getMetrics(): Promise<MetricInfo[]> {
    if (!this.metricList) {
      this.metricList = this.getResource<MetricInfo[]>('metrics').catch((err) => {
        this.metricList = undefined;
        throw err;
      });
    }
    return this.metricList;
  }

  /** The tags of the listed projects, for the tag dropdowns. */
  getTags(): Promise<string[]> {
    if (!this.tagList) {
      this.tagList = this.getResource<string[]>('tags').catch((err) => {
        this.tagList = undefined;
        throw err;
      });
    }
    return this.tagList;
  }

  getBranches(project: string): Promise<BranchInfo[]> {
    return this.getResource<BranchInfo[]>('branches', { project });
  }
}
