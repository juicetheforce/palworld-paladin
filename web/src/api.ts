// Thin typed client for Paladin's own API (not the game's REST API).
export interface StatusResponse {
  server_name: string;
  description: string;
  version: string;
  world_guid: string;
  online: boolean;
  fps: number;
  fps_average: number;
  frame_time_ms: number;
  players: number;
  max_players: number;
  in_game_time?: string;
  in_game_days?: number;
  bases: number;
  days: number;
  uptime_sec: number;
  backup_count: number;
  error?: string;
}

export type SessionState = "needs_setup" | "needs_login" | "authenticated";

export interface HostSnapshot {
  time: string;
  cpu_model: string;
  cpu_cores: number;
  cpu_mhz: number;
  cpu_usage: number;
  cpu_hottest_core: number;
  cpu_steal: number;
  is_vm: boolean;
  cpu_available: boolean;
  mem_total: number;
  mem_available: number;
  mem_used: number;
  swap_total: number;
  swap_used: number;
  cpu_temp: number;
  temp_available: boolean;
  net_interface: string;
  net_rx_bps: number;
  net_tx_bps: number;
  net_available: boolean;
  available?: boolean; // false when no host provider wired
}

export interface RosterPlayer {
  name: string;
  user_id: string;
  online: boolean;
  level: number;
  ping: number;
  guild: string | null; // reserved for save-parsing tier
  bases: number | null;  // reserved for save-parsing tier
}

export interface PlayersResponse {
  online: boolean;
  players: RosterPlayer[];
  history_tier: boolean; // false until save parsing exists
  error?: string;
}

export interface BanEntry {
  user_id: string;
  raw: string;
}

export interface BackupInfo {
  id: string;
  trigger: string;
  created: string;
  size_bytes: number;
}

export interface UpdateCheckResponse {
  local_buildid: string;
  remote_buildid?: string;
  update_available: boolean;
  checked_at?: string;
  checking: boolean;
  error?: string;
}

export interface MemRestartConfig {
  enabled: boolean;
  threshold_gb: number;
  broadcast: string;
  delay_seconds: number;
}

export interface MemRestartResponse {
  available: boolean;
  config?: MemRestartConfig;
  current_memory_bytes?: number;
}

export interface SettingsKey {
  key: string;
  category: string;
  type: "bool" | "float" | "int" | "string" | "enum" | "list";
  default: unknown;
  min?: number | null;
  max?: number | null;
  enum?: string[];
  tooltip: string;
  gotcha?: string | null;
  protected?: string | null;
}

export interface HistoryEntry {
  time: string;
  action: string;
  detail: string;
  ok: boolean;
}

async function j<T>(r: Response): Promise<T> {
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    throw new Error((body as any).error || `HTTP ${r.status}`);
  }
  return r.json() as Promise<T>;
}

export const api = {
  session: () => fetch("/api/session").then(j<{ state: SessionState; username?: string }>),
  setup: (password: string) =>
    fetch("/api/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    }).then(j<{ ok: boolean }>),
  login: (password: string) =>
    fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    }).then(j<{ ok: boolean }>),
  logout: () => fetch("/api/logout", { method: "POST" }).then(j<{ ok: boolean }>),
  status: () => fetch("/api/status").then(j<StatusResponse>),
  host: () => fetch("/api/host").then(j<HostSnapshot>),
  players: () => fetch("/api/players").then(j<PlayersResponse>),
  bans: () => fetch("/api/bans").then(j<{ bans: BanEntry[] }>),
  playerAction: (action: "kick" | "ban" | "unban", user_id: string, message = "") =>
    fetch(`/api/players/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_id, message }),
    }).then(j<{ ok: boolean }>),

  // Server Admin
  lifecycle: (action: "start" | "stop" | "restart", broadcast = "", delay_seconds = 0) =>
    fetch(`/api/admin/lifecycle/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ broadcast, delay_seconds }),
    }).then(j<{ ok: boolean }>),
  broadcast: (message: string) =>
    fetch("/api/admin/broadcast", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message }),
    }).then(j<{ ok: boolean }>),
  save: () => fetch("/api/admin/save", { method: "POST" }).then(j<{ ok: boolean }>),
  adminBackups: () => fetch("/api/admin/backups").then(j<{ backups: BackupInfo[]; partials: number }>),
  deleteBackup: (id: string) => fetch(`/api/admin/backups/${id}`, { method: "DELETE" }).then(j<{ ok: boolean }>),
  createBackup: () => fetch("/api/admin/backups", { method: "POST" }).then(j<{ accepted: boolean }>),
  deleteBackups: (ids: string[]) =>
    fetch("/api/admin/backups/delete-batch", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ids }),
    }).then(j<{ deleted: number; failed: Record<string, string> }>),
  restoreBackup: (id: string, broadcast = "", delay_seconds = 0) =>
    fetch("/api/admin/backups/restore", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, broadcast, delay_seconds }),
    }).then(j<{ accepted: boolean }>),
  history: () => fetch("/api/admin/history").then(j<{ history: HistoryEntry[] }>),
  settings: () => fetch("/api/admin/settings").then(j<{ keys: SettingsKey[]; values: Record<string, string> }>),
  commitSettings: (changes: Record<string, string>, broadcast = "", delay_seconds = 0) =>
    fetch("/api/admin/settings/commit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ changes, broadcast, delay_seconds }),
    }).then(j<{ accepted: boolean; staged: number }>),
  memRestart: () => fetch("/api/admin/mem-restart").then(j<MemRestartResponse>),
  setMemRestart: (config: MemRestartConfig) =>
    fetch("/api/admin/mem-restart", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    }).then(j<{ ok: boolean; config: MemRestartConfig }>),
  updateCheck: () => fetch("/api/admin/update-check").then(j<UpdateCheckResponse>),
  updateCheckRefresh: () => fetch("/api/admin/update-check", { method: "POST" }).then(j<{ accepted: boolean }>),
  update: (broadcast = "", delay_seconds = 0) =>
    fetch("/api/admin/update", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ broadcast, delay_seconds }),
    }).then(j<{ accepted: boolean }>),
};
