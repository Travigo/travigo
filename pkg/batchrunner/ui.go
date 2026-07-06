package batchrunner

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Travigo Batch</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6f7f8;
      --panel: #ffffff;
      --text: #1d252c;
      --muted: #607080;
      --line: #d9e0e5;
      --accent: #0f766e;
      --accent-strong: #115e59;
      --danger: #b42318;
      --warn: #a15c00;
      --ok: #1f7a3f;
      --code: #111827;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    header {
      height: 56px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 20px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }
    h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 700;
      letter-spacing: 0;
    }
    main {
      display: grid;
      grid-template-columns: minmax(280px, 360px) minmax(0, 1fr);
      min-height: calc(100vh - 56px);
    }
    aside {
      border-right: 1px solid var(--line);
      background: var(--panel);
      overflow: auto;
    }
    section {
      padding: 18px;
    }
    h2 {
      margin: 0 0 12px;
      font-size: 15px;
      font-weight: 700;
    }
    h3 {
      margin: 16px 0 8px;
      font-size: 13px;
      color: var(--muted);
      text-transform: uppercase;
    }
    button {
      border: 1px solid var(--line);
      border-radius: 6px;
      background: #fff;
      color: var(--text);
      min-height: 34px;
      padding: 0 12px;
      font: inherit;
      cursor: pointer;
    }
    button.primary {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
      font-weight: 700;
    }
    button.primary:hover { background: var(--accent-strong); }
    button.danger {
      color: var(--danger);
      border-color: #efb5ad;
    }
    button:disabled {
      opacity: .55;
      cursor: not-allowed;
    }
    input[type="number"] {
      width: 74px;
      height: 34px;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 0 8px;
      font: inherit;
    }
    label {
      display: flex;
      align-items: center;
      gap: 8px;
      min-height: 28px;
      color: var(--text);
    }
    .toolbar {
      display: flex;
      gap: 8px;
      align-items: center;
      flex-wrap: wrap;
    }
    .muted { color: var(--muted); }
    .run-list {
      display: grid;
      gap: 6px;
    }
    .run-row {
      width: 100%;
      text-align: left;
      display: grid;
      gap: 2px;
      padding: 9px 10px;
      border-radius: 6px;
    }
    .run-row.active {
      border-color: var(--accent);
      background: #eefaf7;
    }
    .status {
      display: inline-flex;
      align-items: center;
      min-height: 22px;
      padding: 0 8px;
      border-radius: 999px;
      font-size: 12px;
      font-weight: 700;
      background: #edf1f5;
      color: #394957;
    }
    .status.succeeded { background: #e9f7ee; color: var(--ok); }
    .status.failed { background: #fff0ee; color: var(--danger); }
    .status.running { background: #fff6e5; color: var(--warn); }
    .status.cancelled, .status.skipped { background: #f0f1f2; color: #67727d; }
    .layout {
      display: grid;
      grid-template-columns: minmax(300px, 430px) minmax(0, 1fr);
      gap: 18px;
      align-items: start;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 14px;
    }
    .fieldset {
      display: grid;
      gap: 8px;
      margin-bottom: 14px;
    }
    .dataset-list {
      display: grid;
      gap: 2px;
      max-height: 340px;
      overflow: auto;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 8px;
      background: #fbfcfd;
    }
    .dataset-list label {
      align-items: flex-start;
      padding: 2px 0;
    }
    .dataset-meta {
      display: block;
      color: var(--muted);
      font-size: 12px;
      overflow-wrap: anywhere;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
    }
    th, td {
      border-bottom: 1px solid var(--line);
      padding: 8px 10px;
      text-align: left;
      vertical-align: top;
    }
    th {
      font-size: 12px;
      color: var(--muted);
      background: #fbfcfd;
    }
    tr:last-child td { border-bottom: 0; }
    .task-row {
      cursor: pointer;
    }
    .task-row.selected {
      background: #eefaf7;
    }
    pre {
      min-height: 280px;
      max-height: 520px;
      overflow: auto;
      margin: 0;
      padding: 12px;
      border-radius: 8px;
      background: var(--code);
      color: #e5e7eb;
      font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    @media (max-width: 960px) {
      main, .layout {
        grid-template-columns: 1fr;
      }
      aside {
        border-right: 0;
        border-bottom: 1px solid var(--line);
        max-height: 260px;
      }
    }
  </style>
</head>
<body>
  <header>
    <h1>Travigo Batch</h1>
    <div class="toolbar">
      <span id="activeRun" class="muted"></span>
      <button id="refresh">Refresh</button>
    </div>
  </header>
  <main>
    <aside>
      <section>
        <h2>Runs</h2>
        <div id="runs" class="run-list"></div>
      </section>
    </aside>
    <section class="layout">
      <div class="panel">
        <h2>New Run</h2>
        <div class="fieldset">
          <label><input id="forceImport" type="checkbox"> Force import</label>
          <label><input id="continueOnFailure" type="checkbox" checked> Continue after failures</label>
          <label>Max active tasks <input id="maxActiveTasks" type="number" min="1" max="64" value="1"></label>
        </div>
        <div class="fieldset">
          <label><input id="linkStops" type="checkbox" checked> Link stops</label>
          <label><input id="linkTransfers" type="checkbox" checked> Build stop transfers</label>
          <label><input id="linkServices" type="checkbox" checked> Link services</label>
          <label><input id="indexStops" type="checkbox" checked> Index stops</label>
        </div>
        <div class="toolbar">
          <button id="selectAll">Select all</button>
          <button id="selectNone">Select none</button>
          <button id="runSelected" class="primary">Run selected</button>
        </div>
        <div id="datasets"></div>
      </div>
      <div>
        <div class="toolbar" style="margin-bottom: 12px;">
          <h2 style="margin: 0;">Run Detail</h2>
          <button id="cancelRun" class="danger" disabled>Cancel</button>
        </div>
        <div id="runDetail" class="muted">No run selected</div>
        <h3>Log</h3>
        <pre id="log"></pre>
      </div>
    </section>
  </main>
  <script>
    const state = {
      plan: null,
      runs: [],
      selectedRunId: null,
      selectedTaskId: null,
      selectedDatasets: new Set(),
      datasetSelectionInitialized: false
    };

    const $ = (id) => document.getElementById(id);
    const API_BASE = (() => {
      const path = window.location.pathname;
      if (path === '/' || path === '') return '';
      return path.endsWith('/') ? path : path + '/';
    })();

    function apiUrl(path) {
      return API_BASE + path.replace(/^\/+/, '');
    }

    async function api(path, options) {
      const response = await fetch(apiUrl(path), options);
      const contentType = response.headers.get('content-type') || '';
      const data = contentType.includes('application/json') ? await response.json() : await response.text();
      if (!response.ok) {
        throw new Error(data.error || response.statusText);
      }
      return data;
    }

    async function load() {
      const [plan, runs] = await Promise.all([api('/api/plan'), api('/api/runs')]);
      state.plan = plan;
      state.runs = runs;
      if (!state.selectedRunId && runs.length) state.selectedRunId = runs[0].id;
      if (!state.datasetSelectionInitialized) {
        for (const size of ['small', 'medium', 'large']) {
          for (const dataset of plan.groups[size] || []) state.selectedDatasets.add(dataset.identifier);
        }
        state.datasetSelectionInitialized = true;
      }
      renderDatasets();
      renderRuns();
      await renderSelectedRun();
    }

    function renderDatasets() {
      const root = $('datasets');
      root.innerHTML = '';
      for (const size of ['small', 'medium', 'large']) {
        const items = state.plan.groups[size] || [];
        const title = document.createElement('h3');
        title.textContent = size + ' (' + items.length + ')';
        root.appendChild(title);
        const list = document.createElement('div');
        list.className = 'dataset-list';
        for (const dataset of items) {
          const label = document.createElement('label');
          const checkbox = document.createElement('input');
          checkbox.type = 'checkbox';
          checkbox.checked = state.selectedDatasets.has(dataset.identifier);
          checkbox.addEventListener('change', () => {
            if (checkbox.checked) state.selectedDatasets.add(dataset.identifier);
            else state.selectedDatasets.delete(dataset.identifier);
          });
          const text = document.createElement('span');
          text.innerHTML = escapeHtml(dataset.identifier) + '<span class="dataset-meta">' + escapeHtml(dataset.format + ' / ' + dataset.provider) + '</span>';
          label.appendChild(checkbox);
          label.appendChild(text);
          list.appendChild(label);
        }
        root.appendChild(list);
      }
    }

    function renderRuns() {
      const root = $('runs');
      root.innerHTML = '';
      for (const run of state.runs) {
        const button = document.createElement('button');
        button.className = 'run-row' + (run.id === state.selectedRunId ? ' active' : '');
        button.innerHTML = '<strong>' + escapeHtml(run.id) + '</strong><span><span class="status ' + run.status + '">' + run.status + '</span> <span class="muted">' + formatDate(run.createdAt) + '</span></span>';
        button.addEventListener('click', async () => {
          state.selectedRunId = run.id;
          state.selectedTaskId = null;
          renderRuns();
          await renderSelectedRun();
        });
        root.appendChild(button);
      }
      const active = state.runs.find((run) => run.status === 'running' || run.status === 'pending');
      $('activeRun').textContent = active ? 'Active: ' + active.id : '';
    }

    async function renderSelectedRun() {
      $('cancelRun').disabled = true;
      if (!state.selectedRunId) {
        $('runDetail').textContent = 'No run selected';
        $('log').textContent = '';
        return;
      }

      const run = await api('/api/runs/' + state.selectedRunId);
      $('cancelRun').disabled = !(run.status === 'running' || run.status === 'pending');
      if (!state.selectedTaskId && run.tasks.length) state.selectedTaskId = run.tasks[0].id;

      const rows = run.tasks.map((task) => {
        const selected = task.id === state.selectedTaskId ? ' selected' : '';
        return '<tr class="task-row' + selected + '" data-task="' + escapeAttr(task.id) + '">' +
          '<td>' + escapeHtml(task.name) + '<div class="muted">' + escapeHtml(task.kind) + '</div></td>' +
          '<td><span class="status ' + task.status + '">' + task.status + '</span></td>' +
          '<td>' + escapeHtml(task.jobName || '') + '</td>' +
          '<td>' + escapeHtml(task.error || '') + '</td>' +
        '</tr>';
      }).join('');

      $('runDetail').innerHTML =
        '<div class="toolbar" style="margin-bottom: 10px;"><span class="status ' + run.status + '">' + run.status + '</span><span class="muted">' + escapeHtml(run.id) + '</span></div>' +
        '<table><thead><tr><th>Task</th><th>Status</th><th>Job</th><th>Error</th></tr></thead><tbody>' + rows + '</tbody></table>';

      document.querySelectorAll('.task-row').forEach((row) => {
        row.addEventListener('click', async () => {
          state.selectedTaskId = row.dataset.task;
          await renderSelectedRun();
        });
      });

      await loadLog();
    }

    async function loadLog() {
      if (!state.selectedRunId || !state.selectedTaskId) {
        $('log').textContent = '';
        return;
      }
      const response = await fetch(apiUrl('/api/runs/' + state.selectedRunId + '/tasks/' + state.selectedTaskId + '/log'));
      $('log').textContent = await response.text();
      $('log').scrollTop = $('log').scrollHeight;
    }

    async function startRun() {
      const options = {
        datasetIds: Array.from(state.selectedDatasets),
        includeAllDatasets: false,
        includeLinkStops: $('linkStops').checked,
        includeTransfers: $('linkTransfers').checked,
        includeLinkServices: $('linkServices').checked,
        includeIndexStops: $('indexStops').checked,
        forceImport: $('forceImport').checked,
        maxActiveTasks: Number($('maxActiveTasks').value || 1),
        continueOnFailure: $('continueOnFailure').checked
      };
      const run = await api('/api/runs', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(options)
      });
      state.selectedRunId = run.id;
      state.selectedTaskId = null;
      await load();
    }

    function formatDate(value) {
      if (!value) return '';
      return new Date(value).toLocaleString();
    }

    function escapeHtml(value) {
      return String(value).replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
    }

    function escapeAttr(value) {
      return escapeHtml(value);
    }

    $('refresh').addEventListener('click', load);
    $('runSelected').addEventListener('click', () => startRun().catch((err) => alert(err.message)));
    $('selectAll').addEventListener('click', () => {
      for (const size of ['small', 'medium', 'large']) {
        for (const dataset of state.plan.groups[size] || []) state.selectedDatasets.add(dataset.identifier);
      }
      renderDatasets();
    });
    $('selectNone').addEventListener('click', () => {
      state.selectedDatasets.clear();
      renderDatasets();
    });
    $('cancelRun').addEventListener('click', async () => {
      if (!state.selectedRunId) return;
      await api('/api/runs/' + state.selectedRunId + '/cancel', { method: 'POST' });
      await load();
    });

    setInterval(async () => {
      const active = state.runs.some((run) => run.status === 'running' || run.status === 'pending');
      if (active || state.selectedRunId) {
        try { await load(); } catch (err) {}
      }
    }, 4000);

    load().catch((err) => {
      $('runDetail').textContent = err.message;
    });
  </script>
</body>
</html>`
