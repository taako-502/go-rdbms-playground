'use strict';
const $ = id => document.getElementById(id);
const state = { tables: [], selected: null, busy: false };
function notice(message, error = false) {
  $('notice').hidden = !message;
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
}
function busy(value) {
  state.busy = value;
  document.querySelectorAll('button,input,select').forEach(el => { el.disabled = value; });
  $('insert').textContent = value ? '処理中…' : '＋ 行を保存';
}
async function request(path, options) {
  const response = await fetch(path, options);
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || 'リクエストに失敗しました。');
  return data;
}
function route() { return `/api/tables/${encodeURIComponent(state.selected.schema.name)}/rows`; }
function renderForm() {
  const schema = state.selected.schema;
  $('fields').replaceChildren();
  for (const column of schema.columns) {
    const wrapper = document.createElement('div'); wrapper.className = 'field';
    const label = document.createElement('label'); label.className = 'field-label'; label.htmlFor = `field-${column.name}`;
    const name = document.createElement('span'); name.textContent = column.name;
    if (column.name === schema.primary_key) { const pk = document.createElement('span'); pk.className = 'pk'; pk.textContent = 'PK'; name.append(pk); }
    const type = document.createElement('span'); type.className = 'field-type'; type.textContent = column.type === 'integer' ? 'int64' : column.type;
    label.append(name, type);
    let input;
    if (column.type === 'boolean') {
      input = document.createElement('select');
      for (const value of ['true', 'false']) { const option = document.createElement('option'); option.value = value; option.textContent = value; input.append(option); }
    } else {
      input = document.createElement('input'); input.type = 'text'; input.autocomplete = 'off';
      if (column.type === 'integer') { input.inputMode = 'numeric'; input.pattern = '-?[0-9]+'; input.required = true; input.placeholder = column.name === schema.primary_key ? '一意の整数' : '整数を入力'; }
      else input.placeholder = '文字列を入力（空文字も可）';
    }
    input.id = `field-${column.name}`; input.name = column.name;
    wrapper.append(label, input); $('fields').append(wrapper);
  }
}
function renderRows(rows, inserted) {
  const schema = state.selected.schema;
  const header = document.createElement('tr');
  for (const column of schema.columns) {
    const th = document.createElement('th'); th.scope = 'col'; th.textContent = column.name;
    if (column.name === schema.primary_key) { const pk = document.createElement('span'); pk.className = 'pk'; pk.textContent = 'PK'; th.append(pk); }
    const type = document.createElement('span'); type.className = 'column-kind'; type.textContent = column.type === 'integer' ? 'int64' : column.type; th.append(type); header.append(th);
  }
  $('columns').replaceChildren(header);
  const fragment = document.createDocumentFragment();
  for (const row of rows) {
    const tr = document.createElement('tr');
    if (inserted && row[schema.primary_key] === inserted[schema.primary_key]) tr.className = 'new-row';
    for (const column of schema.columns) {
      const td = document.createElement('td'); const value = row[column.name]; td.title = String(value);
      if (column.type === 'boolean') { const badge = document.createElement('span'); badge.className = `bool ${value ? '' : 'false'}`; badge.textContent = String(value); td.append(badge); }
      else td.textContent = value === '' ? '""' : String(value);
      tr.append(td);
    }
    fragment.append(tr);
  }
  $('rows').replaceChildren(fragment);
  $('row-count').textContent = rows.length.toLocaleString(); $('shown-count').textContent = `${rows.length.toLocaleString()} rows`;
  $('row-empty').hidden = rows.length !== 0;
  if (inserted) $('rows').querySelector('.new-row')?.scrollIntoView({ block: 'nearest' });
}
async function loadRows(inserted) { const data = await request(route()); renderRows(data.rows, inserted); }
function selectTable(name) {
  state.selected = state.tables.find(table => table.schema.name === name) || state.tables[0] || null;
  $('tables').replaceChildren(); $('table-count').textContent = state.tables.length;
  for (const table of state.tables) {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'table-nav';
    button.textContent = `▦  ${table.schema.name}`; button.classList.toggle('selected', table === state.selected);
    button.setAttribute('aria-current', table === state.selected ? 'page' : 'false');
    button.addEventListener('click', async () => {
      if (state.busy) return;
      selectTable(table.schema.name); busy(true); notice('');
      try { await loadRows(); } catch (error) { notice(error.message, true); } finally { busy(false); }
    });
    $('tables').append(button);
  }
  $('empty-db').hidden = Boolean(state.selected); $('table-content').hidden = !state.selected;
  $('table-name').textContent = state.selected ? state.selected.schema.name : 'データベース';
  if (!state.selected) return;
  $('column-count').textContent = state.selected.schema.columns.length;
  $('index-count').textContent = state.selected.indexes.length;
  $('row-count').textContent = '—'; $('rows').replaceChildren(); $('columns').replaceChildren(); $('row-empty').hidden = true;
  renderForm();
}
async function refresh() {
  if (state.busy) return;
  busy(true); notice('');
  try {
    const current = state.selected?.schema.name;
    const data = await request('/api/tables'); state.tables = data.tables;
    selectTable(current); busy(true);
    if (state.selected) await loadRows();
  } catch (error) { notice(`読み込みに失敗しました。${error.message}`, true); }
  finally { busy(false); }
}
$('refresh').addEventListener('click', refresh);
$('insert-form').addEventListener('submit', async event => {
  event.preventDefault(); if (state.busy || !state.selected) return;
  // Strings preserve every int64 digit; never parse integer inputs with Number.
  const values = Object.fromEntries(new FormData(event.currentTarget));
  busy(true); notice(''); let saved = false;
  try {
    const data = await request(route(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
    saved = true; $('insert-form').reset(); await loadRows(data.row);
    notice('1行を保存しました。一覧に反映されています。');
  } catch (error) {
    notice(saved ? `保存は完了しましたが、一覧を取得できませんでした。再読み込みしてください。${error.message}` : `保存を確認できませんでした。${error.message} 通信エラーの場合は再読み込みしてから再試行してください。`, true);
  } finally { busy(false); }
});
refresh();
