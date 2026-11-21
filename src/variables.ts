import {
  createDataFrame,
  CustomVariableSupport,
  DataFrame,
  DataQueryRequest,
  DataQueryResponse,
  Field,
  FieldType,
} from '@grafana/data';
import { getTemplateSrv } from '@grafana/runtime';
import { Observable, map } from 'rxjs';
import { VariableQueryEditor } from './components/VariableQueryEditor';
import type { DataSource } from './datasource';
import { variableToQuery } from './query';
import { SonarQuery, SonarVariableQuery } from './types';

/**
 * Turns a result into the text/value frame Grafana builds variable options
 * from: the label is the name column and the value is the key column (for
 * branches, both are the branch name).
 */
export function toTextValueFrame(frame: DataFrame): DataFrame {
  const find = (name: string) => frame.fields.find((f: Field) => f.name === name);
  const textField = find('name') ?? frame.fields[0];
  const valueField = find('key') ?? textField;
  const asStrings = (f?: Field) => (f ? f.values.map((v: unknown) => (v === null || v === undefined ? '' : String(v))) : []);
  return createDataFrame({
    refId: frame.refId,
    fields: [
      { name: 'text', type: FieldType.string, values: asStrings(textField) },
      { name: 'value', type: FieldType.string, values: asStrings(valueField) },
    ],
  });
}

export class SonarVariableSupport extends CustomVariableSupport<DataSource, SonarVariableQuery, SonarQuery> {
  editor = VariableQueryEditor;

  constructor(private readonly datasource: DataSource) {
    super();
  }

  query(request: DataQueryRequest<SonarVariableQuery>): Observable<DataQueryResponse> {
    const templateSrv = getTemplateSrv();
    const targets = request.targets.map((t) => variableToQuery(t, (v) => templateSrv.replace(v, request.scopedVars)));
    return this.datasource
      .query({ ...request, targets })
      .pipe(map((resp) => ({ ...resp, data: resp.data.map((f: DataFrame) => toTextValueFrame(f)) })));
  }
}
