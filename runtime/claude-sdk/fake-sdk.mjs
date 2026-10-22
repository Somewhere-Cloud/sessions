// Disposable stdio integration fixture; no network, credentials or subprocesses.
export function query({ prompt, options }) {
  let done;
  let closed = false;
  return {
    async *[Symbol.asyncIterator]() {
      for await (const input of prompt) {
        if (closed) return;
        yield { type: 'system', subtype: 'init', session_id: options.sessionId || options.resume };
        yield { type: 'command_lifecycle', command_uuid: input.uuid, state: 'started' };
        yield { type: 'assistant', parent_tool_use_id: null, user_message_uuid: input.uuid,
          message: { id: input.uuid, role: 'assistant', content: [{ type: 'text', text: 'accepted' }] } };
        yield { type: 'system', subtype: 'task_started', task_id: 'fixture-watcher' };
        yield { type: 'result', subtype: 'success', is_error: false, result: 'done',
          session_id: options.sessionId || options.resume, user_message_uuid: input.uuid };
        yield { type: 'result', subtype: 'success', is_error: false, result: 'background wake' };
      }
      if (!closed) await new Promise(resolve => { done = resolve; });
    },
    async interrupt() {},
    close() { closed = true; done?.(); },
  };
}
