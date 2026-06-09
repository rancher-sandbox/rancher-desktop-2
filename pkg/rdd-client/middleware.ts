import { of } from './gen/rxjsStub';

import type {
  RequestContext,
  ResponseContext,
  ConfigurationOptions,
  ObservableMiddleware,
} from './gen/index';

export function setHeaderMiddleware(key: string, value: string): ObservableMiddleware {
  return {
    pre: (request: RequestContext) => {
      request.setHeaderParam(key, value);
      return of(request);
    },
    post: (response: ResponseContext) => {
      return of(response);
    },
  };
}

// Returns ConfigurationOptions that set a header
export function setHeaderOptions(
  key: string,
  value: string,
  opt?: ConfigurationOptions<ObservableMiddleware>,
): ConfigurationOptions<ObservableMiddleware> {
  const newMiddleware = setHeaderMiddleware(key, value);
  const existingMiddleware = opt?.middleware || [];
  return {
    ...opt,
    middleware:              existingMiddleware.concat(newMiddleware),
    middlewareMergeStrategy: 'append', // preserve chained middleware from opt
  };
}
