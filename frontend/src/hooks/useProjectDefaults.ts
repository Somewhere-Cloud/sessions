import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchProjectDefaults, type ProjectDefaults, type ProjectDefaultsAnswer } from '../api/sessionsd';
import { NO_PROJECT_CWD } from '../lib/projectFolders';
import type { NewSessionTool } from '../lib/newSessionDefaults';
import type { ClaudeSessionOptions } from '../types';

export interface LauncherChoiceSetters {
  chooseTool: (tool: NewSessionTool) => void;
  setClaudeOptions: (update: (current: ClaudeSessionOptions) => ClaudeSessionOptions) => void;
  setCodexModel: (model: string) => void;
  setCodexEffort: (effort: string) => void;
  setAccess: (access: 'full' | 'ask' | 'plan') => void;
}

// applyProjectDefaults pre-fills the launcher from a project's defaults, so
// what the person sees is what will start; anything they change afterwards
// is their explicit choice and wins. Access returns to the built-in full
// access for a project without a permission default.
export function applyProjectDefaults(defaults: ProjectDefaults, set: LauncherChoiceSetters): void {
  const tool: NewSessionTool | null = defaults.provider === 'claude' ? 'claude-code' : defaults.provider === 'codex' ? 'codex' : null;
  if (tool) set.chooseTool(tool);
  if (tool !== 'codex' && (defaults.model || defaults.effort)) {
    set.setClaudeOptions((current) => ({ ...current,
      ...(defaults.model ? { model: defaults.model } : {}),
      ...(defaults.effort ? { effort: defaults.effort as ClaudeSessionOptions['effort'] } : {}) }));
  }
  if (tool !== 'claude-code') {
    if (defaults.model) set.setCodexModel(defaults.model);
    if (defaults.effort) set.setCodexEffort(defaults.effort);
  }
  set.setAccess(defaults.permissions === 'ask' ? 'ask' : 'full');
}

export interface ProjectLaunch {
  answer: ProjectDefaultsAnswer | null;
  error: string | null;
  reload: () => void;
  /** Start in a new worktree: the project's default until changed here. */
  isolated: boolean;
  setIsolated: (on: boolean) => void;
  /** The create-request fields this launch adds. */
  requestFields: { worktree?: true };
}

// useProjectLaunch is the launcher's whole project-defaults surface: it reads
// the project's defaults, pre-fills the launcher's own choices from them, and
// owns the one choice the launcher has no control for, the worktree.
export function useProjectLaunch(machineId: string, cwd: string, enabled: boolean, setters: LauncherChoiceSetters): ProjectLaunch {
  const [isolated, setIsolated] = useState(false);
  const read = useProjectDefaults(machineId, cwd, enabled, (answer) => {
    applyProjectDefaults(answer.defaults, setters);
    setIsolated(answer.defaults.worktree === 'on');
  });
  return { ...read, isolated, setIsolated, requestFields: isolated ? { worktree: true } : {} };
}

// useProjectDefaults reads the defaults of the project the launcher's folder
// belongs to, on the computer the session will start on. A daemon without
// project defaults, or no folder yet, answers null. onLoaded runs once per
// answer, so a launcher can pre-fill from it without re-applying on render.
export function useProjectDefaults(machineId: string, cwd: string, enabled: boolean, onLoaded?: (answer: ProjectDefaultsAnswer) => void): {
  answer: ProjectDefaultsAnswer | null;
  error: string | null;
  reload: () => void;
} {
  const [answer, setAnswer] = useState<ProjectDefaultsAnswer | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [generation, setGeneration] = useState(0);
  const folder = cwd.trim();
  const onLoadedRef = useRef(onLoaded);
  useEffect(() => { onLoadedRef.current = onLoaded; });
  useEffect(() => {
    if (!enabled || !machineId || !folder || folder === NO_PROJECT_CWD) {
      setAnswer(null);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void fetchProjectDefaults(folder, machineId, controller.signal)
        .then((next) => { setAnswer(next); setError(null); if (next) onLoadedRef.current?.(next); })
        .catch((reason) => {
          if (controller.signal.aborted) return;
          setAnswer(null);
          setError(reason instanceof Error ? reason.message : 'Project defaults could not be read.');
        });
    }, 150);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [enabled, machineId, folder, generation]);
  const reload = useCallback(() => setGeneration((current) => current + 1), []);
  return { answer, error, reload };
}
