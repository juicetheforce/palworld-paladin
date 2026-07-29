// A friendly placeholder shown on data pages when the game server isn't
// running — instead of surfacing a raw palapi connection error. The page
// title still renders (via the caller); this replaces the data body.
export function OfflineNotice({ what = "data" }: { what?: string }) {
  return (
    <div className="offline-state">
      <div className="offline-state-icon">◍</div>
      <div className="offline-state-title">Server is stopped</div>
      <div className="offline-state-sub">
        The Palworld server isn't running right now, so live {what} isn't available.
        Start the server from the Server Admin page to see it here.
      </div>
    </div>
  );
}
