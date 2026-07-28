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

async function j<T>(r: Response): Promise<T> {
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    throw new Error((body as any).error || `HTTP ${r.status}`);
  }
  return r.json() as Promise<T>;
}

export const api = {
  session: () => fetch("/api/session").then(j<{ state: SessionState; username?: string }>),
  setup: (username: string, password: string) =>
    fetch("/api/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    }).then(j<{ ok: boolean }>),
  login: (username: string, password: string) =>
    fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    }).then(j<{ ok: boolean }>),
  logout: () => fetch("/api/logout", { method: "POST" }).then(j<{ ok: boolean }>),
  status: () => fetch("/api/status").then(j<StatusResponse>),
};
