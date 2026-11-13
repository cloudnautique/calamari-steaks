// A hidden pane must never change the shared PTY geometry. Observers preserve
// the backend's grid; only the person holding control can fit it to their pane.
export function fittedDimensions({ owned, connected, visible, width, height, proposal }) {
  if (!owned || !connected || !visible || width < 40 || height < 40 || !proposal) return;
  const { cols, rows } = proposal;
  if (!Number.isFinite(cols) || !Number.isFinite(rows) || cols < 2 || rows < 2) return;
  return { cols: Math.min(500,Math.floor(cols)), rows: Math.min(200,Math.floor(rows)) };
}
