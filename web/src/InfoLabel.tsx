// Card label with an optional ⓘ hover-tooltip — the clutter-reduction
// pattern (design pass): explanatory prose lives at the cursor, not in
// the card body.
export function InfoLabel({ label, info }: { label: string; info?: string }) {
  return (
    <div className="card-label">
      {label}
      {info && <span className="info-tip" title={info}>ⓘ</span>}
    </div>
  );
}
