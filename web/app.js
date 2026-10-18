const $ = (id) => document.getElementById(id);
const api = (path, opts = {}) =>
  fetch('/api/v1/llms' + path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  }).then(async (r) => {
    if (r.status === 204) return null;
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body?.error?.message || r.statusText);
    return body;
  });

let items = [];

function toast(msg, ok = true) {
  const t = $('toast');
  t.textContent = msg;
  t.className = ok ? 'ok' : 'bad';
  t.hidden = false;
  clearTimeout(toast._h);
  toast._h = setTimeout(() => (t.hidden = true), 3000);
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

async function load() {
  const q = new URLSearchParams();
  if ($('q').value.trim()) q.set('q', $('q').value.trim());
  if ($('enabled').value) q.set('enabled', $('enabled').value);
  items = (await api('?' + q)).items || [];
  render();
}

function render() {
  $('empty').hidden = items.length > 0;
  $('rows').innerHTML = items
    .map(
      (it) => `<tr data-id="${esc(it.id)}">
  <td>${esc(it.name)}</td>
  <td>${esc(it.provider)}</td>
  <td class="mono">${esc(it.model_id)}</td>
  <td class="num">${it.context_length || ''}</td>
  <td><button class="toggle ${it.enabled ? 'on' : 'off'}" data-act="toggle">${it.enabled ? 'yes' : 'no'}</button></td>
  <td>${(it.tags || []).map((t) => `<span class="tag">${esc(t)}</span>`).join(' ')}</td>
  <td class="rowbtns">
    <button data-act="edit">edit</button>
    <button data-act="del" class="danger">delete</button>
  </td>
</tr>`
    )
    .join('');
}

$('rows').addEventListener('click', async (e) => {
  const btn = e.target.closest('button');
  if (!btn) return;
  const id = e.target.closest('tr').dataset.id;
  const it = items.find((x) => x.id === id);
  const act = btn.dataset.act;
  try {
    if (act === 'toggle') {
      await api('/' + id, { method: 'PATCH', body: JSON.stringify({ enabled: !it.enabled }) });
      load();
    } else if (act === 'edit') {
      fillForm(it);
    } else if (act === 'del') {
      if (!confirm(`delete "${it.name}"?`)) return;
      await api('/' + id, { method: 'DELETE' });
      if ($('f-id').value === id) cancelEdit();
      toast(`deleted ${it.name}`);
      load();
    }
  } catch (err) {
    toast(err.message, false);
  }
});

function fillForm(it) {
  $('form-title').textContent = 'edit entry: ' + it.name;
  $('f-id').value = it.id;
  $('f-name').value = it.name;
  $('f-provider').value = it.provider;
  $('f-model-id').value = it.model_id;
  $('f-endpoint').value = it.endpoint || '';
  $('f-context').value = it.context_length || 0;
  $('f-enabled').checked = !!it.enabled;
  $('f-tags').value = (it.tags || []).join(', ');
  $('cancel').hidden = false;
  $('form-sec').scrollIntoView({ behavior: 'smooth' });
}

function cancelEdit() {
  $('form').reset();
  $('f-id').value = '';
  $('f-enabled').checked = true;
  $('form-title').textContent = 'create entry';
  $('cancel').hidden = true;
  $('form-error').hidden = true;
}

$('form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const id = $('f-id').value;
  const payload = {
    name: $('f-name').value.trim(),
    provider: $('f-provider').value.trim(),
    model_id: $('f-model-id').value.trim(),
    endpoint: $('f-endpoint').value.trim(),
    context_length: +$('f-context').value || 0,
    enabled: $('f-enabled').checked,
    tags: $('f-tags').value.split(',').map((s) => s.trim()).filter(Boolean),
  };
  try {
    if (id) {
      await api('/' + id, { method: 'PUT', body: JSON.stringify(payload) });
      toast(`updated ${payload.name}`);
    } else {
      await api('', { method: 'POST', body: JSON.stringify(payload) });
      toast(`created ${payload.name}`);
    }
    cancelEdit();
    load();
  } catch (err) {
    $('form-error').textContent = err.message;
    $('form-error').hidden = false;
  }
});

$('cancel').addEventListener('click', cancelEdit);
$('refresh').addEventListener('click', load);
$('q').addEventListener('input', () => (clearTimeout(load._h), load._h = setTimeout(load, 250)));
$('enabled').addEventListener('change', load);

load().catch((err) => toast(err.message, false));
