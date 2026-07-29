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
};
