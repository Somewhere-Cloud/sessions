import { getServer } from '../../lib/servers';
import { featureJSON, httpBaseForServer, serverFetch } from './core';

export interface StorageBytes {
  logical: number;
  allocated: number | null;
  shared_logical: number;
  files: number;
  symlinks: number;
  confidence: 'observed' | 'partial';
}
export interface StorageCandidate {
  path: string;
  category: string;
  action: 'kept' | 'potentially-reclaimable';
  reasons: string[];
  restore?: string;
  bytes: StorageBytes;
}
export interface StorageRoot {
  path: string;
  ownership: 'known' | 'unknown';
  sessions: string[];
  git: { dirty: string; untracked: string; unpushed: string };
  references: { path: string; session: string; pid: number }[];
  bytes: StorageBytes;
  categories: Record<string, StorageBytes>;
  candidates: StorageCandidate[];
  issues: string[];
}
export interface StorageReport {
  machine_id: string;
  machine_name: string;
  preview: boolean;
  complete: boolean;
  roots: StorageRoot[];
  totals: StorageBytes;
  issues: string[];
  limitations: string[];
  scanned_at: number;
}
export async function fetchStoragePreview(serverId: string, signal: AbortSignal): Promise<StorageReport> {
  const server = getServer(serverId);
  const response = await serverFetch(server, `${httpBaseForServer(server)}/api/storage/preview`, { signal });
  return featureJSON<StorageReport>(response, 'Storage inventory');
}
