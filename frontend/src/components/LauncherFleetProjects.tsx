import { useMemo, useState } from 'react';
import { useFleetProjects } from '../hooks/useFleetProjects';
import { fleetProjectChoices } from '../lib/projectAgents';
import { serverDisplayName, type ServerConfig } from '../lib/servers';
import { MachineMark } from './MachineMark';
import { Select } from './ui/Field';

// LauncherFleetProjects lists each project once however many computers have
// it, with a computer choice. Choosing starts on the computer being looked at
// when it has the project, else on the one with the most recent session
// there; the folder is that computer's own checkout, nothing moves.
export function LauncherFleetProjects({ machines, machineId, cwd, onChoose }: {
  machines: ServerConfig[]; machineId: string; cwd: string;
  onChoose: (serverId: string, path: string) => void;
}): JSX.Element | null {
  const { projects } = useFleetProjects(machines, machines.length > 1);
  const choices = useMemo(() => fleetProjectChoices(projects, machineId), [projects, machineId]);
  const [picked, setPicked] = useState<Record<string, string>>({});
  if (machines.length < 2 || choices.length === 0) return null;
  const nameOf = (serverId: string): string => {
    const server = machines.find((machine) => machine.id === serverId);
    return server ? serverDisplayName(server, true) : serverId;
  };
  return <div className="recent-workspaces launcher-fleet-projects" aria-label="Projects on your computers">
    {choices.map((choice) => {
      const serverId = picked[choice.identity] ?? choice.defaultServerId;
      const target = choice.machines.find((machine) => machine.serverId === serverId) ?? choice.machines[0];
      return <div key={choice.identity} className="launcher-fleet-project">
        <button type="button" className={machineId === target.serverId && cwd === target.root ? 'is-active' : ''} onClick={() => onChoose(target.serverId, target.root)}
          aria-label={`Project ${choice.name} on ${nameOf(target.serverId)}`} title={`${nameOf(target.serverId)} · ${target.root}`}>
          <span className="workspace-folder-icon" aria-hidden />
          <span className="workspace-card-copy"><strong>{choice.name}</strong><small>{choice.machines.map((machine) => <MachineMark key={machine.serverId} machine={nameOf(machine.serverId)} size={11} />)} {choice.machines.length > 1 ? `On ${choice.machines.length} computers` : nameOf(target.serverId)}</small></span>
          <span className="workspace-radio" aria-hidden />
        </button>
        {choice.machines.length > 1 ? <Select value={serverId} aria-label={`Computer for ${choice.name}`} onChange={(event) => { const next = event.currentTarget.value; setPicked((current) => ({ ...current, [choice.identity]: next })); const there = choice.machines.find((machine) => machine.serverId === next); if (there) onChoose(next, there.root); }}>
          {choice.machines.map((machine) => <option key={machine.serverId} value={machine.serverId}>{nameOf(machine.serverId)}</option>)}
        </Select> : null}
      </div>;
    })}
  </div>;
}
