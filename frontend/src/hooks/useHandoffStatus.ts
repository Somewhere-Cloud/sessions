import { useEffect, useState, type MutableRefObject } from 'react';
import { handoffRequest, type HandoffRecord } from '../api/sessionsd/handoffs';
import { daemonErrorDisplay } from '../api/sessionsd/core';

/** Brief read-only observation. Closing the dialog cancels future reads. */
export function useHandoffStatus(operationId: string | undefined, serverId: string,
  lock: MutableRefObject<boolean>, revision: MutableRefObject<number>, accept: (record: HandoffRecord) => void): string {
  const [notice, setNotice] = useState('');
  useEffect(() => {
    setNotice('');
    if (!operationId) return;
    let live = true;
    let reads = 0;
    let timer: ReturnType<typeof setTimeout>;
    const check = async (): Promise<void> => {
      if (!live) return;
      if (!lock.current) {
        const before = revision.current;
        try {
          const record = await handoffRequest<HandoffRecord>(`/${operationId}`, serverId);
          if (!live) return;
          // An explicit action may have begun while the status read was in flight.
          if (!lock.current && revision.current === before) accept(record);
          if (record.response === 'assistant-observed') return;
        } catch (reason) {
          if (live) setNotice(`Status update paused. ${daemonErrorDisplay(reason) ?? String(reason)} Use Check status to read again.`);
          return;
        }
      }
      reads += 1;
      if (live && reads < 10) timer = setTimeout(() => void check(), 3000);
      else if (live) setNotice('Automatic status checks finished. Use Check status for newer evidence.');
    };
    timer = setTimeout(() => void check(), 3000);
    return () => { live = false; clearTimeout(timer); };
  }, [operationId, serverId, lock, revision, accept]);
  return notice;
}
