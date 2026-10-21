// reason is the runner's own note on a saved message, such as a paused queue.
export type MessageSubmission = void | { queued: true; reason?: string };

/**
 * What the composer says after Claude's runner saved a message instead of
 * starting a turn with it. Saved is not dispatched, and dispatched is not read;
 * nothing here assumes a turn is running or will succeed. The runner's own
 * reason, when it gave one (a paused queue, a turn still open), is the status.
 */
export function queuedComposerNotice(reason?: string): { title: string; detail: string } {
  const status = reason?.trim()
    || "Saved messages are dispatched in order, each only after a turn that ends successfully; after a failed or interrupted turn they wait until you choose Continue saved messages.";
  return { title: 'Saved for next turn', detail: `Claude's runner saved this message; it has not been dispatched to Claude yet. ${status} Do not send it again.` };
}

/** Delivery uncertainty is not a refusal and must never invite a blind resend. */
export class MessageDeliveryError extends Error {
  constructor(message: string, readonly deliveryStatus: 'not-delivered' | 'unknown' | 'text-delivered', readonly operationId: string) {
    super(message);
    this.name = 'MessageDeliveryError';
  }
}
