// A visible pane can fit without taking keyboard control. The server serializes
// geometry with input and defers if another person controls the terminal.
export function fittedDimensions({ connected, visible, width, height, proposal }) {
  if (!connected || !visible || width < 40 || height < 40 || !proposal) return;
  const { cols, rows } = proposal;
  if (!Number.isFinite(cols) || !Number.isFinite(rows) || cols < 2 || rows < 2) return;
  return { cols: Math.min(500,Math.floor(cols)), rows: Math.min(200,Math.floor(rows)) };
}

export function hostHeight(context) {
  const size=context?.containerDimensions;
  const value=size?.height ?? size?.maxHeight;
  return Number.isFinite(value)&&value>0?Math.floor(value):undefined;
}
