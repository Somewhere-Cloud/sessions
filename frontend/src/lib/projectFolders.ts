import { makeProjectFolder } from '../api/sessionsd';

// NO_PROJECT_CWD stands in for a folder the launcher has not made yet. A
// session started with it gets a fresh scratch folder named after its prompt,
// made just before the session starts.
export const NO_PROJECT_CWD = 'sessions:no-project';

// projectFolderName mirrors the daemon's folder naming
// (runtime/internal/project/folders.go), so the preview names the folder
// Sessions will make; a taken name still gets a numeric suffix there.
export function projectFolderName(name: string): string {
  const folder = name.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/-{2,}/g, '-').replace(/^[-.]+|[-.]+$/g, '');
  return folder.slice(0, 64).replace(/[-.]+$/g, '');
}

// startFolder makes the scratch folder for a no-project start and records it
// as the chosen folder, so retrying the same start reuses that folder.
export async function startFolder(cwd: string, prompt: string, serverId: string, choose: (path: string) => void): Promise<string> {
  if (cwd !== NO_PROJECT_CWD) return cwd;
  const made = await makeProjectFolder('scratch', prompt, serverId);
  choose(made.path);
  return made.path;
}
