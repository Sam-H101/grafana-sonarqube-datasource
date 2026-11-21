import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

export type QueryType =
  | 'measureHistory'
  | 'measures'
  | 'issues'
  | 'issueCounts'
  | 'analyses'
  | 'projects'
  | 'metrics'
  | 'branches';

export type Resolution = 'open' | 'resolved' | 'all';

/** One panel query. Mirrors pkg/models/query.go. */
export interface SonarQuery extends DataQuery {
  queryType?: QueryType;
  /** Project keys; an entry may be a dashboard variable such as `$project`. */
  projects?: string[];
  /** Metric keys; an entry may be a dashboard variable. */
  metrics?: string[];
  branch?: string;
  severities?: string[];
  types?: string[];
  resolution?: Resolution;
  createdInRange?: boolean;
  limit?: number;
  groupBy?: string;
  categories?: string[];
}

/** Data source settings (jsonData). The URL is Grafana's standard url field. */
export interface SonarOptions extends DataSourceJsonData {
  /** SonarQube Cloud organization key. */
  organization?: string;
  timeoutSeconds?: number;
  // Read by the Grafana SDK's HTTP client.
  tlsSkipVerify?: boolean;
  tlsAuth?: boolean;
  tlsAuthWithCACert?: boolean;
  serverName?: string;
  /** Route requests through Grafana's secure socks proxy (Private data source connect). */
  enableSecureSocksProxy?: boolean;
  secureSocksProxyUsername?: string;
}

/** Secrets: write-only from the browser, decrypted only in the backend. */
export interface SonarSecureJsonData {
  token?: string;
  tlsCACert?: string;
  tlsClientCert?: string;
  tlsClientKey?: string;
}

/** A template variable query. */
export interface SonarVariableQuery extends DataQuery {
  list?: 'projects' | 'metrics' | 'branches';
  /** For branches: the project whose branches to list. */
  project?: string;
}

export interface ProjectInfo {
  key: string;
  name: string;
}

export interface MetricInfo {
  key: string;
  name: string;
  domain?: string;
  type?: string;
  description?: string;
}

export interface BranchInfo {
  name: string;
  isMain: boolean;
}
