import { findLaneLinks, type LaneLinkTarget } from './laneLink';

// A delegated lane's outcome arrives in its parent as one plain-text message
// (runtime/internal/api/parent_reports.go formatParentReport) so the agent can
// read it. The viewer recognizes that exact shape and shows it as a card.

export type LaneReportOutcome = 'finished' | 'needs input' | 'failed';

export interface LaneReport {
  name: string;
  sessionId: string;
  outcome: LaneReportOutcome;
  body: string;
  lane: LaneLinkTarget;
}

const REPORT_PATTERN = /^\[sessions\] Delegated lane ("(?:[^"\\]|\\.)*") \(([0-9a-f-]{36})\) (finished|needs input|failed):\n([\s\S]*?)\n\nFull result: sessions last \2(?: · Link for the user: (\S+))?\s*$/i;

export function parseLaneReport(text: string | undefined): LaneReport | null {
  const match = text?.match(REPORT_PATTERN);
  if (!match) return null;
  let name: string;
  try {
    name = JSON.parse(match[1]!) as string;
  } catch {
    return null;
  }
  const sessionId = match[2]!.toLowerCase();
  const lane = findLaneLinks(match[5])[0] ?? { sessionId };
  return { name: name || 'Delegated lane', sessionId, outcome: match[3]!.toLowerCase() as LaneReportOutcome, body: match[4]!.trim(), lane };
}
