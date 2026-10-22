import { createHash } from 'node:crypto';

// The durable Go runner owns admission/replay. This bridge holds at most one
// submitted prompt; it never retries a prompt or infers completion from silence.
export class SDKSession {
  constructor(query, options, emit) {
    this.emit = emit;
    this.sessionId = options.resume || options.sessionId;
    this.active = null;
    this.pending = [];
    this.receipts = new Map();
    this.tasks = new Map();
    this.closed = false;
    this.failed = false;
    this.wake = null;
    this.interrupting = false;
    this.provider = query({ prompt: this.inputs(), options });
    this.running = this.consume();
  }

  async *inputs() {
    while (!this.closed) {
      if (!this.pending.length) await new Promise(resolve => { this.wake = resolve; });
      if (this.pending.length) yield this.pending.shift();
    }
  }

  submit(id, text) {
    if (typeof id !== 'string' || !id || typeof text !== 'string' || !text.trim()) {
      throw new Error('A prompt UUID and nonempty text are required');
    }
    const hash = createHash('sha256').update(text).digest('hex');
    const previous = this.receipts.get(id);
    if (previous) {
      if (previous.hash !== hash) throw new Error('Prompt UUID already names different text');
      this.emit({ type: 'accepted', operation_id: id, duplicate: true, phase: previous.phase });
      return;
    }
    if (this.closed || this.failed) throw new Error('SDK transport unavailable; reconcile before recovery');
    if (this.active || this.interrupting) throw new Error('SDK turn or interrupt is still active');
    this.active = { id, consumed: false };
    this.receipts.set(id, { hash, phase: 'submitted' });
    // Only a provider echo/lifecycle event constitutes consumption.
    this.emit({ type: 'accepted', operation_id: id, phase: 'submitted' });
    this.pending.push({ type: 'user', uuid: id, session_id: this.sessionId,
      parent_tool_use_id: null, message: { role: 'user', content: text } });
    this.wake?.();
    this.state();
  }

  state() {
    this.emit({ type: 'state', transport_open: !this.closed && !this.failed,
      ready: !this.closed && !this.failed && !this.active && !this.interrupting,
      active_operation: this.active?.id ?? null,
      provider_consumed: this.active?.consumed ?? false,
      background_tasks: [...this.tasks.values()] });
  }

  observe(message) {
    if (message.session_id && message.session_id !== this.sessionId) {
      throw new Error('SDK event names another provider conversation');
    }
    this.observeTask(message);
    const active = this.active;
    const echoed = message.user_message_uuids ?? [message.user_message_uuid];
    const matches = active && !message.parent_tool_use_id &&
      Array.isArray(echoed) && echoed.includes(active.id);
    const lifecycle = active && message.type === 'command_lifecycle' &&
      message.command_uuid === active.id;
    if (matches || (lifecycle && message.state === 'started')) {
      if (!active.consumed) this.emit({ type: 'consumed', operation_id: active.id });
      active.consumed = true;
      this.receipts.get(active.id).phase = 'consumed';
    }
    // Unbound results belong to background/replayed/unknown turns, never to the
    // current prompt by proximity. Keep them as evidence without settling it.
    const operation = matches || lifecycle ? active.id : null;
    this.emit({ type: 'event', operation_id: operation, event: message });
    if (message.type === 'result' && matches) this.finish(active.id, message);
    this.state();
  }

  observeTask(message) {
    if (message.type !== 'system') return;
    if (message.subtype === 'background_tasks_changed' && Array.isArray(message.tasks)) {
      this.tasks = new Map(message.tasks.map(task => [task.task_id, {
        id: task.task_id, run_id: task.run_id ?? null, status: 'running',
        ambient: task.ambient === true, task_type: task.task_type,
      }]));
      return;
    }
    if (!message.task_id) return;
    const previous = this.tasks.get(message.task_id);
    if (previous?.run_id && message.run_id && message.run_id < previous.run_id) return;
    if (message.subtype === 'task_started' || message.subtype === 'task_progress') {
      this.tasks.set(message.task_id, { id: message.task_id,
        run_id: message.run_id ?? previous?.run_id ?? null,
        tool_use_id: message.tool_use_id ?? null, status: 'running' });
    } else if (message.subtype === 'task_notification') {
      this.tasks.delete(message.task_id);
    }
  }

  finish(id, result) {
    const interrupted = ['aborted_tools', 'aborted_streaming'].includes(result.terminal_reason);
    const failed = result.is_error || result.subtype !== 'success' ||
      [401, 403, 429, 529].includes(result.api_error_status);
    const phase = interrupted ? 'interrupted' : failed ? 'failed' : 'answered';
    this.receipts.get(id).phase = phase;
    this.active = null;
    const settlement = { type: 'settled', operation_id: id, phase, result };
    if (this.interrupting) this.pendingSettlement = settlement;
    else this.emit(settlement);
    // These are only local dedup hints; durable operation receipts live in Go.
    while (this.receipts.size > 64) this.receipts.delete(this.receipts.keys().next().value);
  }

  async interrupt(id) {
    if (!this.active || this.active.id !== id) throw new Error('Interrupt does not name the active prompt');
    this.interrupting = true;
    this.state();
    try {
      await this.provider.interrupt();
      this.emit({ type: 'interrupt_ack', operation_id: id });
    } finally {
      this.interrupting = false;
      if (this.pendingSettlement) {
        this.emit(this.pendingSettlement);
        this.pendingSettlement = null;
      }
      this.state();
    }
    // An interrupt acknowledgement is not a result. Await the matched result.
  }

  async consume() {
    try {
      for await (const message of this.provider) this.observe(message);
      if (!this.closed) throw new Error('SDK stream ended');
    } catch (error) {
      if (!this.closed) {
        this.failed = true;
        this.emit({ type: 'transport_error', operation_id: this.active?.id ?? null,
          error: String(error.message || error), delivery_uncertain: Boolean(this.active) });
        this.state();
      }
    }
  }

  async close() {
    if (this.closed) return this.running;
    this.closed = true;
    this.wake?.();
    this.provider.close();
    await this.running;
    this.state();
  }
}
