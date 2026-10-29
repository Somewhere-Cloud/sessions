import { requestedServer, serverFetch, httpBaseForServer, featureJSON, json } from './core';

export interface HandoffRequest {
  operationId: string;
  sourceSessionId: string;
  destinationProvider: 'claude' | 'codex';
  model: string;
  effort: string;
}
export interface HandoffRecord {
  version: number;
  request: HandoffRequest;
  coverage: { included: number; omitted: number; bytes: number; limitation: string };
  artifact: { sourceHistoryId: string; sourceProvider: string; destinationModel: string };
  targetSessionId?: string;
  creation: string;
  context?: string;
  nativeTargetId?: string;
  prompt?: { status: string; retry: boolean; reason?: string };
  response?: string;
  activity?: string;
  recovery?: string;
  error?: string;
}
export async function handoffRequest<T>(path: string, serverId: string, body?: unknown): Promise<T> {
  const server = requestedServer(serverId);
  const response = await serverFetch(server, `${httpBaseForServer(server)}/api/handoffs${path}`, body === undefined ? {} : {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body)
  });
  // Operation/delivery errors carry recovery evidence, including HTTP 404.
  return path.startsWith('/') && path !== '/preview'
    ? json<T>(response) : featureJSON<T>(response, 'Durable provider switching');
}
