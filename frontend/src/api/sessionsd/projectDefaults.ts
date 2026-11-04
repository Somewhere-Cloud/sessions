import { httpBaseForServer, json, requestedServer, serverFetch } from './core';

/** How a project's new sessions start unless the person chooses otherwise.
 *  The daemon stores them under the project's identity, per computer. */
export interface ProjectDefaults {
  provider?: 'claude' | 'codex';
  model?: string;
  effort?: string;
  worktree?: 'on' | 'off';
  permissions?: 'bypass' | 'ask';
}

export type ProjectDefaultKey = keyof ProjectDefaults;
export const PROJECT_DEFAULT_KEYS: ProjectDefaultKey[] = ['provider', 'model', 'effort', 'worktree', 'permissions'];

export interface ProjectTarget {
  identity: string;
  name: string;
  project_id?: string;
  top_level?: string;
}

export interface ProjectDefaultsAnswer {
  project: ProjectTarget;
  defaults: ProjectDefaults;
}

// fetchProjectDefaults reads the defaults of the project a folder belongs to.
// A daemon without project defaults answers null: there is nothing to apply.
export async function fetchProjectDefaults(cwd: string, serverId: string, signal?: AbortSignal): Promise<ProjectDefaultsAnswer | null> {
  const server = requestedServer(serverId);
  const r = await serverFetch(server, `${httpBaseForServer(server)}/api/projects/defaults?cwd=${encodeURIComponent(cwd)}`, { signal });
  if (r.status === 404 || r.status === 405 || r.status === 501) return null;
  return json<ProjectDefaultsAnswer>(r);
}

// updateProjectDefaults is `sessions projects defaults <folder> ...`: set the
// non-empty fields and clear the named keys.
export async function updateProjectDefaults(cwd: string, set: ProjectDefaults, clear: ProjectDefaultKey[], serverId: string): Promise<ProjectDefaultsAnswer> {
  const server = requestedServer(serverId);
  const r = await serverFetch(server, `${httpBaseForServer(server)}/api/projects/defaults`, {
    method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ cwd, set, clear })
  });
  if (r.status === 404 || r.status === 405 || r.status === 501) {
    throw new Error('This computer runs an older Sessions without project defaults. Update it to save them.');
  }
  return json<ProjectDefaultsAnswer>(r);
}
