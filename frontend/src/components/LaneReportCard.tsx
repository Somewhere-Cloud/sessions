import { useState } from 'react';
import { renderContent } from '../lib/contentRender';
import type { LaneReport } from '../lib/laneReport';
import { LaneLinkButtons } from './WorkLog';
import '../styles/lane-report.css';

const OUTCOME_LABEL: Record<LaneReport['outcome'], string> = {
  finished: 'Finished',
  'needs input': 'Needs input',
  failed: 'Failed'
};

// A delegated lane's outcome as it arrived in this conversation: who, how it
// ended, what it said, and a way into the lane. Long results stay folded.
export function LaneReportCard({ report, cwd = '' }: { report: LaneReport; cwd?: string }): JSX.Element {
  const [open, setOpen] = useState(false);
  const long = report.body.length > 480 || report.body.split('\n').length > 8;
  const tone = report.outcome === 'finished' ? 'done' : report.outcome === 'failed' ? 'failed' : 'attention';
  return (
    <div className={`lane-report is-${tone}`} data-lane-report={report.sessionId}>
      <div className="lane-report-head">
        <span className="lane-report-dot" aria-hidden />
        <strong className="lane-report-name" title={report.sessionId}>{report.name}</strong>
        <span className="lane-report-outcome">{OUTCOME_LABEL[report.outcome]}</span>
        <LaneLinkButtons lanes={[report.lane]} />
      </div>
      <div
        className={`lane-report-body md-content${long && !open ? ' is-folded' : ''}`}
        dangerouslySetInnerHTML={{ __html: renderContent(report.body, cwd) }}
      />
      {long ? (
        <button type="button" className="lane-report-more" onClick={(event) => { event.stopPropagation(); setOpen(!open); }}>
          {open ? 'Show less' : 'Show full report'}
        </button>
      ) : null}
    </div>
  );
}
