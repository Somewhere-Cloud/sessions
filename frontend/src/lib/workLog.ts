import type { ActivityEntry, DispatchMessage, ToolCall } from '../types';
import { previewToolInput } from './toolPreview';

// The work log presents one agent turn's steps: what it thought, which tools
// it ran, and the updates it gave along the way. Claude and Codex feed the
// same ActivityEntry list; this module decides how each step reads.

export type ToolCategory = 'command' | 'read' | 'edit' | 'search' | 'web' | 'agent' | 'mcp' | 'plan' | 'tool';

const COMMAND_TOOLS = new Set(['Bash', 'BashOutput', 'KillBash', 'KillShell', 'Command', 'exec_command', 'shell', 'shell_command', 'local_shell']);
const READ_TOOLS = new Set(['Read', 'NotebookRead', 'LS']);
const EDIT_TOOLS = new Set(['Write', 'Edit', 'MultiEdit', 'NotebookEdit', 'apply_patch']);
const SEARCH_TOOLS = new Set(['Grep', 'Glob', 'ToolSearch']);
const WEB_TOOLS = new Set(['WebFetch', 'WebSearch']);
const AGENT_TOOLS = new Set(['Task', 'Agent', 'SendMessage']);

export function toolCategory(call: ToolCall): ToolCategory {
  const kind = call.kind ?? '';
  if (kind === 'commandExecution' || COMMAND_TOOLS.has(call.name)) return 'command';
  if (kind === 'fileChange' || EDIT_TOOLS.has(call.name)) return 'edit';
  if (kind === 'webSearch' || WEB_TOOLS.has(call.name)) return 'web';
  if (kind === 'mcpToolCall' || call.name.startsWith('mcp__')) return 'mcp';
  if (kind === 'collabAgentToolCall' || AGENT_TOOLS.has(call.name)) return 'agent';
  if (READ_TOOLS.has(call.name)) return 'read';
  if (SEARCH_TOOLS.has(call.name)) return 'search';
  if (call.name === 'TodoWrite' || call.name === 'ExitPlanMode') return 'plan';
  return 'tool';
}

export function isCommandTool(call: ToolCall): boolean {
  return toolCategory(call) === 'command';
}

const RUNNING_STATUSES = new Set(['inprogress', 'running', 'pending']);
const FAILED_STATUSES = new Set(['failed', 'declined', 'error', 'errored']);

/** A call is running while its provider says so, or, for Claude, while the
 *  turn is live and no result has arrived yet. */
export function toolRunning(call: ToolCall, live: boolean): boolean {
  const status = call.status?.toLowerCase() ?? '';
  if (status) return RUNNING_STATUSES.has(status);
  return live && !call.resultFull && !call.failed;
}

export function toolFailed(call: ToolCall): boolean {
  return Boolean(call.failed) || FAILED_STATUSES.has(call.status?.toLowerCase() ?? '');
}

/** A finished step reads in the past tense; a running command reads as what
 *  it is doing ("Run all tests"), not as already done. */
export function toolActivityLabel(call: ToolCall, running = false): string {
  const preview = call.inputPreview?.trim();
  if (isCommandTool(call)) {
    if (running) return preview || 'Running a command';
    return preview ? pastTenseLeadingVerb(preview) : 'Ran a command';
  }
  if (!preview) return displayToolName(call.name);
  switch (call.name) {
    case 'Read': return `Read ${preview}`;
    case 'Write': return `Wrote ${preview}`;
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit': return `Edited ${preview}`;
    case 'Glob': return `Matched files for ${preview}`;
    case 'Grep': return `Searched for ${preview}`;
    case 'WebFetch': return `Fetched ${preview}`;
    case 'WebSearch': return `Searched the web for ${preview}`;
    case 'Task':
    case 'Agent': return `Delegated: ${preview}`;
    default: return `${displayToolName(call.name)} · ${preview}`;
  }
}

/** mcp__server__tool reads as "server · tool". */
export function displayToolName(name: string): string {
  const match = name.match(/^mcp__([^_]+(?:_[^_]+)*)__(.+)$/);
  if (!match) return name;
  return `${match[1]!.replace(/[-_]/g, ' ')} · ${match[2]!.replace(/_/g, ' ')}`;
}

/** First meaningful line of a thinking or update block, for its row. */
export function firstLine(text: string, max = 160): string {
  const line = text.split('\n').map((value) => value.replace(/^[#>*\-\s]+/, '').trim()).find(Boolean) ?? '';
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

const SUMMARY_ORDER: Array<[ToolCategory, string, string]> = [
  ['command', 'ran a command', 'ran {n} commands'],
  ['read', 'read a file', 'read {n} files'],
  ['edit', 'edited a file', 'edited {n} files'],
  ['search', 'searched once', 'searched {n} times'],
  ['web', 'used the web', 'used the web {n} times'],
  ['agent', 'delegated once', 'delegated {n} times'],
  ['mcp', 'used an integration', 'used {n} integrations'],
  ['plan', 'updated the plan', 'updated the plan {n} times'],
  ['tool', 'used a tool', 'used {n} tools']
];

/** One quiet line for a finished group: "Thought, ran 3 commands, read 2 files". */
export function summarizeActivity(entries: ActivityEntry[]): string {
  const counts = new Map<ToolCategory, number>();
  let thought = false;
  let failed = 0;
  for (const entry of entries) {
    if (entry.kind === 'thinking') thought = true;
    if (entry.kind !== 'tool') continue;
    const category = toolCategory(entry.call);
    counts.set(category, (counts.get(category) ?? 0) + 1);
    if (toolFailed(entry.call)) failed += 1;
  }
  const parts: string[] = thought ? ['thought'] : [];
  for (const [category, one, many] of SUMMARY_ORDER) {
    const count = counts.get(category) ?? 0;
    if (count > 0) parts.push(count === 1 ? one : many.replace('{n}', String(count)));
  }
  if (parts.length === 0) {
    const updates = entries.filter((entry) => entry.kind === 'update').length;
    parts.push(updates === 1 ? 'gave an update' : `gave ${updates} updates`);
  }
  if (failed > 0) parts.push(failed === 1 ? '1 failed' : `${failed} failed`);
  const text = parts.join(', ');
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** "40s", "3m 05s", "1h 02m". */
export function formatElapsed(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`;
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`;
}

/** The ordered steps a message shows, from its own activity or, for records
 *  converted before activity existed, from its tool calls. */
export function messageActivity(message: DispatchMessage): { lead: ActivityEntry[]; trail: ActivityEntry[] } {
  if (message.activity && message.activity.length > 0) {
    const lead = Math.min(message.activityLead ?? message.activity.length, message.activity.length);
    return { lead: message.activity.slice(0, lead), trail: message.activity.slice(lead) };
  }
  const tools: ActivityEntry[] = (message.toolCalls ?? []).map((call) => ({ kind: 'tool', id: call.id, call }));
  return { lead: [], trail: tools };
}

const TOOL_PAST_TENSE = new Map<string, string>([
  ['add', 'Added'], ['build', 'Built'], ['check', 'Checked'], ['copy', 'Copied'],
  ['create', 'Created'], ['delegate', 'Delegated'], ['delete', 'Deleted'], ['edit', 'Edited'], ['fetch', 'Fetched'],
  ['find', 'Found'], ['inspect', 'Inspected'], ['install', 'Installed'], ['list', 'Listed'],
  ['merge', 'Merged'], ['move', 'Moved'], ['read', 'Read'], ['regenerate', 'Regenerated'],
  ['remove', 'Removed'], ['resolve', 'Resolved'], ['run', 'Ran'], ['search', 'Searched'],
  ['see', 'Saw'], ['show', 'Showed'], ['test', 'Tested'], ['update', 'Updated'],
  ['verify', 'Verified'], ['write', 'Wrote']
]);

function pastTenseLeadingVerb(value: string): string {
  const match = value.match(/^([A-Za-z]+)(\b.*)$/);
  if (!match) return value;
  const replacement = TOOL_PAST_TENSE.get(match[1]!.toLowerCase());
  if (!replacement) return value;
  const rest = match[2]!.replace(/^ and ([A-Za-z]+)\b/i, (full, verb: string) => {
    const second = TOOL_PAST_TENSE.get(verb.toLowerCase());
    return second ? ` and ${second.toLowerCase()}` : full;
  });
  return `${replacement}${rest}`;
}

/** A saved-history tool step (GET /api/history/<id>/preview) as a work-log row. */
export interface SavedToolStep {
  id: string;
  name: string;
  input?: Record<string, unknown>;
  result?: string;
  failed?: boolean;
}

export function savedToolActivity(steps: SavedToolStep[] | undefined): ActivityEntry[] {
  return (steps ?? []).map((step, index) => {
    let inputFull: string | undefined;
    if (step.input) {
      try { inputFull = JSON.stringify(step.input, null, 2); } catch { /* leave empty */ }
    }
    const call: ToolCall = {
      id: step.id || `saved-${index}`,
      name: step.name,
      inputPreview: previewToolInput(step.name, step.input),
      inputFull,
      resultFull: step.result,
      failed: step.failed
    };
    return { kind: 'tool', id: call.id, call };
  });
}
