// Renders a layer-by-layer connection diagnosis (address, identity, relay,
// peer, admission, server, project) so "timeout" and "not enrolled" read
// differently. Text only; nothing here is sent to chat.
const labels = { address: 'Address', runtime: 'Native library', identity: 'Identity', bind: 'Network', relay: 'Relay', connect: 'Peer', stream: 'Stream', admission: 'Admission', server: 'Server', route: 'Route' };
const marks = { ok: '✓', warn: '!', failed: '✗', skipped: '–', unknown: '?' };

export function stepRows(steps) {
  return (steps || []).filter(step => step.status !== 'skipped').map(step => ({
    status: marks[step.status] ? step.status : 'unknown',
    mark: marks[step.status] || marks.unknown,
    label: labels[step.layer] || step.layer,
    summary: step.summary || '',
    hint: step.status === 'failed' || step.status === 'warn' ? step.hint || '' : '',
  }));
}

export function renderSteps(list, steps) {
  if (!list) return;
  const rows = stepRows(steps);
  list.replaceChildren();
  list.hidden = !rows.length;
  for (const row of rows) {
    const item = document.createElement('li'); item.className = row.status;
    const mark = document.createElement('span'); mark.textContent = row.mark; mark.setAttribute('aria-label', row.status);
    const label = document.createElement('strong'); label.textContent = row.label;
    const summary = document.createElement('span'); summary.textContent = row.summary;
    item.append(mark, label, summary);
    if (row.hint) { const hint = document.createElement('span'); hint.className = 'hint'; hint.textContent = row.hint; item.append(hint); }
    list.append(item);
  }
}
