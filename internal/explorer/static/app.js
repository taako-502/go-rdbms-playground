'use strict';
const $ = id => document.getElementById(id);
const state = { tables: [], selected: null, busy: false, view: 'records', primaryKeys: null };
const viewButtons = document.querySelectorAll('[data-view]');
const booleanValues = ['true', 'false'];
function notice(message, error = false) {
  $('notice').hidden = !message;
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
}
function busy(value) {
  state.busy = value;
  document.querySelectorAll('button,input,select').forEach(el => { el.disabled = value; });
  $('random-fill').disabled = value || state.primaryKeys === null;
  $('insert').textContent = value ? '処理中…' : '＋ 行を保存';
}
async function request(path, options) {
  const response = await fetch(path, options);
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || 'リクエストに失敗しました。');
  return data;
}
function tableRoute() { return `/api/tables/${encodeURIComponent(state.selected.schema.name)}`; }
const columnType = type => type === 'integer' ? 'int64' : type === 'boolean' ? 'bool' : type;

function definitionRows(target, rows) {
  target.replaceChildren(...rows.map(([name, ...values]) => {
    const row = document.createElement('tr');
    const heading = document.createElement('th');
    heading.scope = 'row';
    heading.textContent = name;
    row.append(heading);
    for (const value of values) {
      const cell = document.createElement('td');
      cell.textContent = value;
      row.append(cell);
    }
    return row;
  }));
}

function renderDefinitions(indexColumn) {
  const { schema, indexes } = state.selected;
  definitionRows($('schema-columns'), schema.columns.map(column => [
    column.name, columnType(column.type), column.name === schema.primary_key ? 'はい' : '—', '不可',
  ]));
  definitionRows($('index-definitions'), indexes.map(column => [
    column, 'B-tree', column === schema.primary_key ? '主キー（自動）' : 'セカンダリ',
    column === schema.primary_key ? 'はい' : 'いいえ',
  ]));
  $('index-column').replaceChildren(...indexes.map(column => new Option(column, column)));
  $('index-column').value = indexes.includes(indexColumn) ? indexColumn : schema.primary_key;
  $('tree-summary').textContent = '';
  $('index-tree').replaceChildren();
}

function renderTreeNode(node, columnType, isRoot = false) {
  const branch = document.createElement('li');
  const card = document.createElement('div');
  card.className = 'tree-node';
  const label = document.createElement('div');
  label.className = 'tree-node-label';
  const children = node.children ?? [];
  label.textContent = isRoot ? (children.length ? 'ルート' : 'ルート兼葉') : (children.length ? '内部ノード' : '葉');
  card.append(label);
  const entries = document.createElement('div');
  entries.className = 'tree-entries';
  for (const entry of node.entries) {
    const details = document.createElement('details');
    const key = document.createElement('summary');
    key.textContent = columnType === 'string' ? JSON.stringify(entry.key) : String(entry.key);
    const offsets = document.createElement('div');
    offsets.className = 'tree-offsets';
    offsets.textContent = `バイト位置 ${entry.offsets.join(', ')}`;
    details.append(key, offsets);
    entries.append(details);
  }
  if (node.entries.length === 0) entries.textContent = 'キーなし';
  card.append(entries);
  branch.append(card);
  if (children.length) {
    const list = document.createElement('ul');
    for (const child of children) list.append(renderTreeNode(child, columnType));
    branch.append(list);
  }
  return branch;
}

async function loadIndex() {
  const target = $('index-tree');
  const summary = $('tree-summary');
  const column = $('index-column').value;
  summary.textContent = '';
  target.textContent = '読み込み中…';
  try {
    const index = await request(`${tableRoute()}/indexes/${encodeURIComponent(column)}`);
    const tree = document.createElement('ul');
    tree.className = 'tree-root';
    tree.append(renderTreeNode(index.root, index.column.type, true));
    summary.textContent = `最小次数 ${index.degree} · ノードあたり最大 ${2 * Number(index.degree) - 1} キー`;
    target.replaceChildren(tree);
    target.scrollTo(0, 0);
  } catch (error) {
    target.textContent = error.message;
  }
}

function showView(view) {
  state.view = view;
  for (const button of viewButtons) {
    const active = button.dataset.view === view;
    button.setAttribute('aria-pressed', String(active));
    $(button.getAttribute('aria-controls')).hidden = !active;
  }
}

async function showIndex() {
  busy(true);
  try { await loadIndex(); } finally { busy(false); }
}

for (const button of viewButtons) {
  button.addEventListener('click', () => {
    showView(button.dataset.view);
    if (state.view === 'indexes') void showIndex();
  });
}
$('index-column').addEventListener('change', showIndex);
function renderForm() {
  const schema = state.selected.schema;
  $('fields').replaceChildren();
  for (const column of schema.columns) {
    const wrapper = document.createElement('div'); wrapper.className = 'field';
    const label = document.createElement('label'); label.className = 'field-label'; label.htmlFor = `field-${column.name}`;
    const name = document.createElement('span'); name.textContent = column.name;
    if (column.name === schema.primary_key) { const pk = document.createElement('span'); pk.className = 'pk'; pk.textContent = 'PK'; name.append(pk); }
    const type = document.createElement('span'); type.className = 'field-type'; type.textContent = columnType(column.type);
    label.append(name, type);
    let input;
    if (column.type === 'boolean') {
      input = document.createElement('select');
      for (const value of booleanValues) { const option = document.createElement('option'); option.value = value; option.textContent = value; input.append(option); }
    } else {
      input = document.createElement('input'); input.type = 'text'; input.autocomplete = 'off';
      if (column.type === 'integer') { input.inputMode = 'numeric'; input.pattern = '-?[0-9]+'; input.required = true; input.placeholder = column.name === schema.primary_key ? '一意の整数' : '整数を入力'; }
      else input.placeholder = '文字列を入力（空文字も可）';
    }
    input.id = `field-${column.name}`; input.name = column.name;
    wrapper.append(label, input); $('fields').append(wrapper);
  }
}
function fillRandomValues() {
  if (state.busy || state.primaryKeys === null) return;
  const schema = state.selected.schema;
  const primary = schema.columns.find(column => column.name === schema.primary_key);
  const randomInteger = () => Math.floor(Math.random() * 1000) + 1;
  const randomText = () => `sample-${Math.floor(Math.random() * 36 ** 6).toString(36).padStart(6, '0')}`;
  let primaryValue;
  switch (primary.type) {
    case 'integer': {
      let value = randomInteger();
      while (state.primaryKeys.has(String(value))) value++;
      primaryValue = String(value);
      break;
    }
    case 'string': {
      const prefix = randomText();
      primaryValue = prefix;
      for (let suffix = 1; state.primaryKeys.has(primaryValue); suffix++) {
        primaryValue = `${prefix}-${suffix}`;
      }
      break;
    }
    case 'boolean': {
      const available = booleanValues.filter(value => !state.primaryKeys.has(value));
      if (available.length === 0) {
        notice('主キーの true と false はどちらも使用済みです。', true);
        return;
      }
      primaryValue = available[Math.floor(Math.random() * available.length)];
      break;
    }
  }
  for (const column of schema.columns) {
    const field = $(`field-${column.name}`);
    if (column === primary) {
      field.value = primaryValue;
      continue;
    }
    switch (column.type) {
      case 'integer': field.value = String(randomInteger()); break;
      case 'string': field.value = randomText(); break;
      case 'boolean': field.value = booleanValues[Math.floor(Math.random() * booleanValues.length)]; break;
    }
  }
  notice('');
}
function renderRows(rows, inserted) {
  const schema = state.selected.schema;
  state.primaryKeys = new Set(rows.map(row => String(row[schema.primary_key])));
  const header = document.createElement('tr');
  for (const column of schema.columns) {
    const th = document.createElement('th'); th.scope = 'col'; th.textContent = column.name;
    if (column.name === schema.primary_key) { const pk = document.createElement('span'); pk.className = 'pk'; pk.textContent = 'PK'; th.append(pk); }
    const type = document.createElement('span'); type.className = 'column-kind'; type.textContent = columnType(column.type); th.append(type); header.append(th);
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
async function loadRows(inserted) { const data = await request(`${tableRoute()}/rows`); renderRows(data.rows, inserted); }
async function loadTable() {
  if (state.view === 'indexes') await loadIndex();
  await loadRows();
}
function selectTable(name) {
  const indexColumn = state.selected?.schema.name === name ? $('index-column').value : null;
  state.primaryKeys = null;
  state.selected = state.tables.find(table => table.schema.name === name) || state.tables[0] || null;
  $('tables').replaceChildren(); $('table-count').textContent = state.tables.length;
  for (const table of state.tables) {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'table-nav';
    button.textContent = `▦  ${table.schema.name}`; button.classList.toggle('selected', table === state.selected);
    button.setAttribute('aria-current', table === state.selected ? 'page' : 'false');
    button.addEventListener('click', async () => {
      if (state.busy) return;
      selectTable(table.schema.name); busy(true); notice('');
      try { await loadTable(); } catch (error) { notice(error.message, true); } finally { busy(false); }
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
  renderDefinitions(indexColumn);
}
async function refresh() {
  if (state.busy) return;
  busy(true); notice('');
  try {
    const current = state.selected?.schema.name;
    const data = await request('/api/tables'); state.tables = data.tables;
    selectTable(current); busy(true);
    if (state.selected) await loadTable();
  } catch (error) { notice(`読み込みに失敗しました。${error.message}`, true); }
  finally { busy(false); }
}
$('refresh').addEventListener('click', refresh);
$('random-fill').addEventListener('click', fillRandomValues);
$('insert-form').addEventListener('submit', async event => {
  event.preventDefault(); if (state.busy || !state.selected) return;
  // Strings preserve every int64 digit; never parse integer inputs with Number.
  const values = Object.fromEntries(new FormData(event.currentTarget));
  busy(true); notice(''); let saved = false;
  try {
    const data = await request(`${tableRoute()}/rows`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) });
    state.primaryKeys?.add(String(data.row[state.selected.schema.primary_key]));
    saved = true; $('insert-form').reset(); await loadRows(data.row);
    notice('1行を保存しました。一覧に反映されています。');
  } catch (error) {
    notice(saved ? `保存は完了しましたが、一覧を取得できませんでした。再読み込みしてください。${error.message}` : `保存を確認できませんでした。${error.message} 通信エラーの場合は再読み込みしてから再試行してください。`, true);
  } finally { busy(false); }
});
refresh();
