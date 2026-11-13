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

export function widgetHeight(context, catalog, viewportHeight) {
  const available=hostHeight(context);
  // Missing displayMode is treated as inline. A host's generous maximum is
  // permission to use space, not a request to fill the conversation thread.
  if(catalog || context?.displayMode!=='fullscreen')return Math.min(520,available ?? 520);
  const fixed=context?.containerDimensions?.height;
  if(Number.isFinite(fixed)&&fixed>0)return Math.floor(fixed);
  const viewport=Number.isFinite(viewportHeight)&&viewportHeight>0?Math.floor(viewportHeight):720;
  return Math.min(viewport,available ?? viewport);
}
