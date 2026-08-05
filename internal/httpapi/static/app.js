function amatoken() {
  return {
    tab: 'dashboard',
    // Per-tab filters: Dashboard and Sessions keep independent state so
    // changing one never affects the other.
    filters: {
      dashboard: { range: '30d', from: '', to: '', project: '', model: '' },
      sessions:  { range: 'all', from: '', to: '', project: '', model: '', search: '' },
      savings:   { range: '30d', from: '', to: '' },
    },
    options: { projects: [], models: [] },
    summary: {},
    series: [],
    sessions: [],
    total: 0,
    page: 1,
    limit: 50,
    sessionsSort: 'last_seen_desc',
    rankings: { projects: [], models: [] },
    pricing: [],
    modelCatalog: [],
    pricingSearch: '',
    pricingSource: '',
    pricingProvider: '',
    pricingSort: 'price_desc',
    pricingPage: 1,
    pricingLimit: 25,
    pricingStatus: null,
    pricingCreateOpen: false,
    syncing: false,
    refreshing: false,
    refreshState: null,    // null | 'success' | 'error'
    syncState: null,       // null | 'success' | 'error'
    importingSession: false,
    importSessionState: null,
    comparison: null,      // { cost_usd, sessions, messages, input_tokens, ... } as % delta
    comparisonLabel: '',   // human label like "vs previous 7 days" or "vs last month"
    budgets: [],
    newBudget: { name: '', amount_usd: 0 },
    drilldown: { open: false, loading: false, deleting: false, records: [], session: null },
    metricDetail: { open: false, title: '', subtitle: '', firstHeader: '', rows: [] },
    confirmModal: { open: false, title: '', message: '', confirmLabel: 'Delete', onConfirm: null },
    sessionDeleteModal: { open: false, session: null, loading: false, deleting: false, confirmText: '', preview: null, error: '' },
    autoRefresh: true,
    autoSync: true,
    autoRefreshTimer: null,
    showResources: true,
    res: { goroutines: 0, memoryMB: 0, cpuPct: 0, memPct: 0, hostCPU: 0, hostMemMB: 0 },
    memUnit: 'pct',  // 'pct' | 'mb' — toggle for memory display in the header
    metricCards: [
      { key: 'cost_usd',              label: 'Cost (USD)',    field: 'cost_usd',              formatted: v => (v ?? 0).toLocaleString('en-US', { style:'currency', currency:'USD', minimumFractionDigits:2, maximumFractionDigits:2 }) },
      { key: 'sessions',              label: 'Sessions',      field: 'sessions',              formatted: v => (v ?? 0).toLocaleString() },
      { key: 'messages',              label: 'Messages',      field: 'messages',              formatted: v => (v ?? 0).toLocaleString() },
      { key: 'input_tokens',          label: 'Input tokens',  field: 'input_tokens',          formatted: v => (v ?? 0).toLocaleString() },
      { key: 'output_tokens',         label: 'Output tokens', field: 'output_tokens',         formatted: v => (v ?? 0).toLocaleString() },
      { key: 'cache_creation_tokens', label: 'Cache write',   field: 'cache_creation_tokens', formatted: v => (v ?? 0).toLocaleString() },
      { key: 'cache_read_tokens',     label: 'Cache read',    field: 'cache_read_tokens',     formatted: v => (v ?? 0).toLocaleString() },
    ],
    newPricing: { model: '', input_per_mtok_usd: 0, output_per_mtok_usd: 0, cache_write_per_mtok_usd: 0, cache_read_per_mtok_usd: 0 },
    chart: null,
    rtkChart: null,
    rtkSummary: null,
    rtkTimeseries: [],
    rtkCommands: [],
    rtkTrend: null,
    rtkCommandFilter: null,
    rtkModal: { open: false, title: '', subtitle: '', firstHeader: 'Date', rows: [] },

    async init() {
      await this.loadFilterOptions();
      await this.loadBudgets();
      await this.loadAutomationSettings();
      await this.reload();
      await this.loadPricing();
      await this.loadModelCatalog();
      this.pollResources();
    },

    askConfirm(title, message, onConfirm, confirmLabel = 'Delete') {
      this.confirmModal = { open: true, title, message, confirmLabel, onConfirm };
    },
    confirmYes() {
      const fn = this.confirmModal.onConfirm;
      this.confirmModal.open = false;
      if (typeof fn === 'function') fn();
    },
    confirmNo() {
      this.confirmModal.open = false;
    },

    sessionExportFilename(session) {
      if (!session) return 'session.tgz';
      const path = this.formatPath(session.cwd || session.project_slug || 'session');
      const parts = String(path).split('/').filter(Boolean);
      const leaf = (parts[parts.length - 1] || 'session').replace(/[^a-zA-Z0-9._-]+/g, '-');
      const d = session.last_seen ? new Date(session.last_seen) : new Date();
      const pad = n => String(n).padStart(2, '0');
      const stamp = `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`;
      return `${leaf}-${stamp}.amatoken-session.tgz`;
    },
    exportAllSessionsFilename() {
      const d = new Date();
      const pad = n => String(n).padStart(2, '0');
      const stamp = `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`;
      return `amatoken-all-sessions-${stamp}.tgz`;
    },
    exportAllSessions() {
      const a = document.createElement('a');
      const filename = this.exportAllSessionsFilename();
      a.href = `/api/sessions/export-all?filename=${encodeURIComponent(filename)}`;
      a.download = filename;
      a.rel = 'noopener';
      document.body.appendChild(a);
      a.click();
      a.remove();
    },
    triggerSessionImport() {
      if (this.importingSession || !this.$refs.sessionImport) return;
      this.$refs.sessionImport.value = '';
      this.$refs.sessionImport.click();
    },
    async importSessionFile(event) {
      const file = event.target.files && event.target.files[0];
      if (!file) return;
      this.importingSession = true;
      this.importSessionState = null;
      try {
        const fd = new FormData();
        fd.append('file', file);
        const r = await fetch('/api/sessions/import', { method:'POST', body: fd });
        if (!r.ok) {
          this.askConfirm('Could not import session bundle', await r.text() || 'Server rejected the import.', () => {}, 'OK');
          this.importSessionState = 'error';
          return;
        }
        const res = await r.json().catch(() => ({}));
        await this.loadFilterOptions();
        await this.reload();
        const artifactBits = [];
        if (res.bundle) {
          if (res.history_lines_added) artifactBits.push(`${res.history_lines_added} history line(s)`);
          if (res.session_env_files) artifactBits.push(`${res.session_env_files} session-env file(s)`);
          if (res.file_history_files) artifactBits.push(`${res.file_history_files} file-history file(s)`);
        }
        const importedLabel = res.full_bundle ? 'All sessions bundle imported' : (res.bundle ? 'Session bundle imported' : 'Session imported');
        const importedVerb = res.full_bundle ? 'Restored machine bundle' : (res.bundle ? 'Restored' : 'Imported');
        const importedScope = res.full_bundle && res.project_files ? ` · project files: ${res.project_files}` : '';
        this.askConfirm(
          importedLabel,
          `${importedVerb} ${res.line_count || 0} line(s) from ${file.name}${res.session_ids?.length ? ` · session(s): ${res.session_ids.join(', ')}` : ''}${importedScope}${artifactBits.length ? ` · extras: ${artifactBits.join(', ')}` : ''}.`,
          () => {},
          'OK',
        );
        this.importSessionState = 'success';
      } catch (_) {
        this.importSessionState = 'error';
      } finally {
        this.importingSession = false;
        event.target.value = '';
        setTimeout(() => { this.importSessionState = null; }, 2500);
      }
    },

    closeSessionDeleteModal() {
      this.sessionDeleteModal = { open: false, session: null, loading: false, deleting: false, confirmText: '', preview: null, error: '', backup: false };
    },
    async openSessionDeleteModal(session) {
      if (!session || !session.session_id) return;
      this.sessionDeleteModal = { open: true, session, loading: true, deleting: false, confirmText: '', preview: null, error: '', backup: false };
      try {
        const r = await fetch(`/api/sessions/${encodeURIComponent(session.session_id)}/delete-preview`);
        if (!r.ok) {
          this.sessionDeleteModal.error = await r.text() || 'Could not inspect session artifacts.';
          return;
        }
        this.sessionDeleteModal.preview = await r.json();
      } catch (_) {
        this.sessionDeleteModal.error = 'Could not inspect session artifacts.';
      } finally {
        this.sessionDeleteModal.loading = false;
      }
    },
    async confirmSessionDelete() {
      const modal = this.sessionDeleteModal;
      const session = modal.session;
      if (!session || !session.session_id) return;
      if (modal.confirmText !== 'DELETE') {
        modal.error = 'Type DELETE to confirm.';
        return;
      }
      modal.deleting = true;
      modal.error = '';
      try {
        const r = await fetch(`/api/sessions/${encodeURIComponent(session.session_id)}`, {
          method:'DELETE',
          headers:{'Content-Type':'application/json'},
          body: JSON.stringify({ confirm: 'DELETE', backup: !!modal.backup }),
        });
        if (!r.ok) {
          modal.error = await r.text() || 'Server rejected the request.';
          return;
        }
        const res = await r.json().catch(() => null);
        await this.loadFilterOptions();
        await this.reload();
        this.closeDrilldown();
        this.closeSessionDeleteModal();
        if (res && res.files && res.files.length) {
          const first = res.files[0]?.backup_path || '';
          this.askConfirm(
            'Session deleted',
            res.backup ? `Session removed from ${res.file_count || res.files.length} artifact(s). Backup created${first ? `: ${first}` : '.'}` : `Session removed from ${res.file_count || res.files.length} artifact(s) without backup.`,
            () => {},
            'OK',
          );
        }
      } finally {
        if (this.sessionDeleteModal.open) this.sessionDeleteModal.deleting = false;
      }
    },

    async loadAutomationSettings() {
      const settings = await fetch('/api/settings').then(r=>r.json()).catch(() => ({}));
      // Default to true unless explicitly disabled.
      this.autoRefresh = settings.auto_refresh_enabled !== 'false';
      this.autoSync = settings.pricing_auto_sync !== 'false';
      this.applyAutoRefresh();
    },
    async toggleAutoRefresh() {
      // Alpine has already mutated this.autoRefresh by the time this runs.
      await fetch('/api/settings', {
        method:'PUT', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({ key: 'auto_refresh_enabled', value: this.autoRefresh ? 'true' : 'false' }),
      });
      this.applyAutoRefresh();
    },
    applyAutoRefresh() {
      if (this.autoRefreshTimer) { clearInterval(this.autoRefreshTimer); this.autoRefreshTimer = null; }
      if (this.autoRefresh) {
        // Poll every 60s — matches the server-side reconcile interval, so the
        // client never lags noticeably behind even when fsnotify is quiet.
        this.autoRefreshTimer = setInterval(() => this.refresh(), 60000);
      }
    },
    async toggleAutoSync() {
      await fetch('/api/settings', {
        method:'PUT', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({ key: 'pricing_auto_sync', value: this.autoSync ? 'true' : 'false' }),
      });
    },

    fmtBytes(n) {
      if (!n) return '0 B';
      const units = ['B', 'KB', 'MB', 'GB', 'TB'];
      let u = 0, v = n;
      while (v >= 1024 && u < units.length - 1) { v /= 1024; u++; }
      return v.toFixed(u === 0 ? 0 : 1) + ' ' + units[u];
    },

    async pollResources() {
      try {
        const r = await fetch('/api/resources').then(rex => rex.json()).catch(() => null);
        if (r) {
          this.res.goroutines = r.goroutines || 0;
          this.res.memoryMB   = r.memoryMB   || 0;
          this.res.memPct     = r.memory_pct_host || 0;
          this.res.cpuPct     = r.cpu_pct_host    || 0;
          this.res.hostMemMB  = r.host_memory_total_mb || 0;
          this.res.hostCPU    = r.host_cpu_count || 0;
        }
      } catch (_) {}
      setTimeout(() => this.pollResources(), 3000);
    },
    toggleMemUnit() { this.memUnit = this.memUnit === 'pct' ? 'mb' : 'pct'; },

    hasBranch(b) {
      // Empty string and "HEAD" (detached HEAD on a checked-out commit) both
      // indicate no real branch context worth showing.
      return !!b && b !== 'HEAD';
    },
    deltaClass(pct) {
      if (pct === null || pct === undefined || !isFinite(pct)) return '';
      // Cost / messages going up vs prior period is "bad" (more spend); we
      // colour up = red, down = green. For a usage tool, more is more cost.
      return pct > 0 ? 'up' : pct < 0 ? 'down' : '';
    },
    deltaText(pct, fmtVal) {
      if (pct === null || pct === undefined) return '';
      if (!isFinite(pct)) return 'new';
      const arrow = pct > 0 ? '▲' : pct < 0 ? '▼' : '·';
      return `${arrow} ${Math.abs(pct).toFixed(1)}% vs prev`;
    },
    goHome() {
      this.tab = 'dashboard';
      this.filters.dashboard = { range: '30d', from: '', to: '', project: '', model: '' };
      this.page = 1;
      this.reload();
    },

    // Helper: returns the filter object for a given tab. Defaults to dashboard
    // when called from non-dashboard/sessions contexts (e.g. modals).
    f(tab) { return this.filters[tab] || this.filters.dashboard; },

    // For most filters: previous window of identical length immediately
    // before. For "All time": fall back to comparing this calendar month
    // against the previous calendar month — gives a meaningful delta even
    // when no period is selected.
    prevRangeBounds() {
      const cur = this.rangeBounds('dashboard');
      const fromStr = cur.from;
      if (!fromStr) {
        const now = new Date();
        const thisMonth = new Date(now.getFullYear(), now.getMonth(), 1);
        const prevMonth = new Date(now.getFullYear(), now.getMonth() - 1, 1);
        return {
          from: prevMonth.toISOString(),
          to: thisMonth.toISOString(),
          curFrom: thisMonth.toISOString(),
          label: 'vs last month',
        };
      }
      const from = new Date(fromStr);
      const to = cur.to ? new Date(cur.to) : new Date();
      const len = to.getTime() - from.getTime();
      if (len <= 0) return null;
      const days = Math.round(len / 86400000);
      return {
        from: new Date(from.getTime() - len).toISOString(),
        to: from.toISOString(),
        label: `vs previous ${days <= 1 ? '24 hours' : days + ' days'}`,
      };
    },
    rangeBounds(scope = 'dashboard') {
      const f = this.f(scope);
      const now = new Date();
      const iso = d => d.toISOString();
      switch (f.range) {
        case '24h':    return { from: iso(new Date(now.getTime() - 24*3600*1000)) };
        case '7d':     return { from: iso(new Date(now.getTime() - 7*24*3600*1000)) };
        case '30d':    return { from: iso(new Date(now.getTime() - 30*24*3600*1000)) };
        case 'month':  return { from: iso(new Date(now.getFullYear(), now.getMonth(), 1)) };
        case 'custom': {
          const out = {};
          if (f.from) out.from = f.from;
          if (f.to)   out.to   = f.to;
          return out;
        }
        case 'all':
        default:       return {};
      }
    },
    // Hour bucket only for the 24h preset on the dashboard; everything else uses daily.
    chartBucket() { return this.filters.dashboard.range === '24h' ? 'hour' : 'day'; },
    // qs() always uses the DASHBOARD filters — dashboard widgets (summary,
    // chart, rankings, comparison) show dashboard's view independently of
    // whatever the user is doing in the Sessions tab.
    qs() {
      const f = this.filters.dashboard;
      const p = new URLSearchParams();
      const b = this.rangeBounds('dashboard');
      if (b.from) p.set('from', b.from);
      if (b.to)   p.set('to',   b.to);
      if (f.project) p.set('project', f.project);
      if (f.model)   p.set('model',   f.model);
      return p.toString();
    },
    // sessionsQS() always uses the SESSIONS filters — independent of the
    // dashboard. Includes the free-text search.
    sessionsQS() {
      const f = this.filters.sessions;
      const p = new URLSearchParams();
      const b = this.rangeBounds('sessions');
      if (b.from) p.set('from', b.from);
      if (b.to)   p.set('to',   b.to);
      if (f.project) p.set('project', f.project);
      if (f.model)   p.set('model',   f.model);
      if (f.search)  p.set('q',       f.search);
      if (this.sessionsSort && this.sessionsSort !== 'last_seen_desc') p.set('sort', this.sessionsSort);
      return p.toString();
    },

    rtkQS() {
      const p = new URLSearchParams();
      const b = this.rangeBounds('savings');
      if (b.from) p.set('from', this.toDateParam(b.from));
      if (b.to)   p.set('to',   this.toDateParam(b.to));
      return p.toString();
    },
    toDateParam(value) {
      if (!value) return '';
      if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
      const d = new Date(value);
      if (Number.isNaN(d.getTime())) return '';
      const pad = n => String(n).padStart(2, '0');
      return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}`;
    },
    searchChanged() {
      this.page = 1; // search shrinks the result set; jump back to page 1.
      this.reload();
    },
    sessionsSortChanged() {
      this.page = 1;
      this.reload();
    },
    fmtUSD(v) { return (v ?? 0).toLocaleString('en-US', { style:'currency', currency:'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 }); },

    // Translate Claude Code's project slug "-home-bedatty-foo" → "/home/bedatty/foo".
    // Original components containing dashes are lost in this encoding (a Claude
    // Code limitation), so we prefer the per-record cwd when available.
    formatPath(s) {
      if (!s) return '';
      if (s.startsWith('/')) return s;
      if (!s.startsWith('-')) return s;
      return '/' + s.slice(1).replace(/-/g, '/');
    },

    async loadFilterOptions() {
      const r = await fetch('/api/filters'); this.options = await r.json();
    },
    async reload() {
      const qs = this.qs();
      const offset = (this.page - 1) * this.limit;
      const bucket = this.chartBucket();
      const [s, ts, ss, rp, rm] = await Promise.all([
        fetch(`/api/summary?${qs}`).then(r=>r.json()),
        fetch(`/api/timeseries?bucket=${bucket}&${qs}`).then(r=>r.json()),
        fetch(`/api/sessions?limit=${this.limit}&offset=${offset}&${this.sessionsQS()}`).then(r=>r.json()),
        fetch(`/api/rankings/projects?${qs}`).then(r=>r.json()),
        fetch(`/api/rankings/models?${qs}`).then(r=>r.json()),
      ]);
      this.summary = s;
      this.series = ts || [];
      this.sessions = ss.rows || [];
      this.total = ss.total || 0;
      this.rankings = { projects: rp || [], models: rm || [] };
      this.renderChart(bucket);
      await this.loadComparison();
      await this.loadBudgets();
    },
    get pages() { return Math.max(1, Math.ceil(this.total / this.limit)); },
    pageWindow() {
      const total = this.pages, cur = this.page, span = 2;
      const start = Math.max(1, cur - span);
      const end = Math.min(total, cur + span);
      const out = [];
      for (let i = start; i <= end; i++) out.push(i);
      return out;
    },
    goPage(n) {
      n = Math.max(1, Math.min(this.pages, n));
      if (n === this.page) return;
      this.page = n;
      this.reload();
    },
    async refresh() {
      this.refreshing = true;
      this.refreshState = null;
      let ok = false;
      try {
        const r = await fetch('/api/ingest/refresh', { method:'POST' });
        ok = r.ok;
        await this.loadFilterOptions();
        await this.reload();
      } catch (_) {
        ok = false;
      } finally {
        this.refreshing = false;
        this.flashState('refreshState', ok ? 'success' : 'error');
      }
    },
    flashState(key, value) {
      this[key] = value;
      // Clear after 2.5s so the icon doesn't linger forever.
      setTimeout(() => { this[key] = null; }, 2500);
    },

    async loadComparison() {
      // Comparison cards live on the dashboard — always built from the
      // dashboard filter context.
      const dash = this.filters.dashboard;
      const prev = this.prevRangeBounds();
      if (!prev) { this.comparison = null; this.comparisonLabel = ''; return; }
      const p = new URLSearchParams();
      p.set('from', prev.from);
      p.set('to',   prev.to);
      if (dash.project) p.set('project', dash.project);
      if (dash.model)   p.set('model',   dash.model);

      // For "All time" we compare current calendar month vs previous month, so
      // the "current" side is also a fixed window — not the unbounded summary
      // we already have. Fetch it explicitly.
      const curRequests = [fetch(`/api/summary?${p.toString()}`).then(r=>r.json()).catch(() => null)];
      let curSummary = this.summary?.summary;
      if (prev.curFrom) {
        const cp = new URLSearchParams();
        cp.set('from', prev.curFrom);
        if (dash.project) cp.set('project', dash.project);
        if (dash.model)   cp.set('model',   dash.model);
        curRequests.push(fetch(`/api/summary?${cp.toString()}`).then(r=>r.json()).catch(() => null));
      }
      const [prevSum, curSum] = await Promise.all(curRequests);
      if (!prevSum) { this.comparison = null; this.comparisonLabel = ''; return; }
      if (curSum) curSummary = curSum.summary;
      const cur = curSummary || {};
      const old = prevSum.summary || {};
      const pct = (a, b) => {
        if (b === 0 || b === undefined || b === null) return a > 0 ? Infinity : 0;
        return ((a - b) / b) * 100;
      };
      this.comparison = {
        cost_usd:              pct(cur.cost_usd ?? 0,              old.cost_usd ?? 0),
        sessions:              pct(cur.sessions ?? 0,              old.sessions ?? 0),
        messages:              pct(cur.messages ?? 0,              old.messages ?? 0),
        input_tokens:          pct(cur.input_tokens ?? 0,          old.input_tokens ?? 0),
        output_tokens:         pct(cur.output_tokens ?? 0,         old.output_tokens ?? 0),
        cache_creation_tokens: pct(cur.cache_creation_tokens ?? 0, old.cache_creation_tokens ?? 0),
        cache_read_tokens:     pct(cur.cache_read_tokens ?? 0,     old.cache_read_tokens ?? 0),
      };
      this.comparisonLabel = prev.label || '';
    },

    async loadBudgets() {
      const fetched = await fetch('/api/budgets').then(r=>r.json()).catch(() => []) || [];
      // Preserve transient per-row UI state (saving, save result icon) across
      // refreshes so the user keeps seeing their last save outcome.
      const prev = new Map((this.budgets || []).map(b => [b.id, b]));
      this.budgets = fetched.map(b => {
        const old = prev.get(b.id);
        return { ...b, _saving: old?._saving || false, _saveState: old?._saveState || null };
      });
    },
    async addBudget() {
      if (!this.newBudget.name || !this.newBudget.amount_usd) return;
      await fetch('/api/budgets', {
        method:'POST', headers:{'Content-Type':'application/json'},
        body: JSON.stringify(this.newBudget),
      });
      this.newBudget = { name: '', amount_usd: 0 };
      await this.loadBudgets();
    },
    async saveBudget(b) {
      // Enforce the 5-pin maximum here on the client. Server stays authoritative
      // by accepting any value; the cap is purely a UX nudge.
      if (b.show_in_dashboard) {
        const pinned = this.budgets.filter(x => x.show_in_dashboard && x.id !== b.id).length;
        if (pinned >= 5) {
          b.show_in_dashboard = false;
          this.askConfirm('Pin limit reached',
            'You can pin at most 5 budgets to the dashboard. Unpin one before adding another.',
            () => {}, 'OK');
          return;
        }
      }
      b._saving = true; b._saveState = null;
      let ok = false;
      try {
        const r = await fetch(`/api/budgets/${b.id}`, {
          method:'PUT', headers:{'Content-Type':'application/json'},
          body: JSON.stringify({ name: b.name, amount_usd: b.amount_usd, show_in_dashboard: !!b.show_in_dashboard }),
        });
        ok = r.ok;
      } catch (_) { ok = false; }
      finally {
        b._saving = false;
        b._saveState = ok ? 'success' : 'error';
        await this.loadBudgets();
        // Reload replaces row references; the timeout therefore needs to find
        // the live row by id rather than mutating the stale `b` from above.
        const rowId = b.id;
        setTimeout(() => {
          const live = this.budgets.find(x => x.id === rowId);
          if (live) live._saveState = null;
        }, 2500);
      }
    },
    deleteBudget(b, i) {
      this.askConfirm(
        'Delete budget?',
        `"${b.name}" ($${b.amount_usd}) will be removed permanently. This action cannot be undone.`,
        async () => {
          await fetch(`/api/budgets/${b.id}`, { method:'DELETE' });
          this.budgets.splice(i, 1);
        },
      );
    },
    budgetClass(pct) {
      if (pct >= 100) return 'over';
      if (pct >= 80) return 'warn';
      return 'ok';
    },
    dashboardBudgets() {
      return (this.budgets || []).filter(b => b.show_in_dashboard);
    },

    filterByProject(slug) {
      // Toggle: clicking the same project again clears the filter, so the
      // ranking row also acts as a "deselect" without leaving the dashboard.
      // Rankings live on the dashboard — clicking a row toggles the
      // dashboard's project filter, never touches Sessions filters.
      const f = this.filters.dashboard;
      f.project = f.project === slug ? '' : slug;
      this.page = 1;
      this.reload();
    },
    filterByModel(model) {
      const f = this.filters.dashboard;
      f.model = f.model === model ? '' : model;
      this.page = 1;
      this.reload();
    },

    openMetricDetail(key) {
      const card = this.metricCards.find(c => c.key === key);
      if (!card) return;
      const sumVal = this.summary?.summary?.[card.field] ?? 0;
      const models = this.summary?.models || [];
      const projects = this.rankings?.projects || [];
      let firstHeader = 'Model', source = models, valueOf, formatter = card.formatted;
      // For session/messages cards, breaking down by project is more
      // intuitive than by model.
      if (key === 'sessions' || key === 'messages') {
        firstHeader = 'Project';
        source = projects;
        valueOf = key === 'sessions' ? p => p.sessions : p => p.messages;
      } else {
        valueOf = m => m[card.field] ?? (key === 'cost_usd' ? m.cost_usd : 0);
      }
      const labelOf = key === 'sessions' || key === 'messages'
        ? p => this.formatPath(p.cwd || p.project_slug)
        : m => m.model;

      const total = sumVal || source.reduce((a, x) => a + (valueOf(x) || 0), 0) || 1;
      const rows = source
        .map(x => {
          const v = valueOf(x) || 0;
          return { label: labelOf(x), value: v, formatted: formatter(v), pct: (v / total) * 100 };
        })
        .filter(r => r.value > 0)
        .sort((a, b) => b.value - a.value)
        .slice(0, 15);

      this.metricDetail = {
        open: true,
        title: `${card.label}: ${formatter(sumVal)}`,
        subtitle: this.comparisonLabel || '',
        firstHeader,
        rows,
      };
    },
    closeMetricDetail() {
      this.metricDetail.open = false;
    },

    async openBucketDetail(index) {
      const p = this.series[index];
      if (!p) return;
      const start = new Date(p.bucket);
      const end = new Date(start);
      const isHour = this.chartBucket() === 'hour';
      if (isHour) end.setHours(end.getHours() + 1); else end.setDate(end.getDate() + 1);
      const qs = new URLSearchParams();
      qs.set('from', start.toISOString());
      qs.set('to',   end.toISOString());
      const dash = this.filters.dashboard;
      if (dash.project) qs.set('project', dash.project);
      if (dash.model)   qs.set('model',   dash.model);

      const sum = await fetch(`/api/summary?${qs.toString()}`).then(r => r.json()).catch(() => null);
      const models = sum?.models || [];
      const total = models.reduce((acc, m) => acc + (m.cost_usd || 0), 0) || 1;
      const fmtUSD = v => (v ?? 0).toLocaleString('en-US', { style:'currency', currency:'USD', minimumFractionDigits:2, maximumFractionDigits:2 });
      const titleDate = isHour
        ? start.toLocaleString([], { weekday:'short', month:'short', day:'2-digit', hour:'2-digit', minute:'2-digit' })
        : start.toLocaleDateString([], { weekday:'long', year:'numeric', month:'long', day:'2-digit' });

      const rows = models
        .map(m => ({
          label: m.model,
          value: m.cost_usd || 0,
          formatted: fmtUSD(m.cost_usd || 0),
          pct: ((m.cost_usd || 0) / total) * 100,
        }))
        .filter(r => r.value > 0)
        .sort((a, b) => b.value - a.value);

      this.metricDetail = {
        open: true,
        title: `${titleDate} — ${fmtUSD(p.cost_usd || 0)}`,
        subtitle: `${(p.input_tokens + p.output_tokens + p.cache_creation_tokens + p.cache_read_tokens).toLocaleString()} tokens · ${(sum?.summary?.sessions ?? 0)} sessions · ${(sum?.summary?.messages ?? 0)} messages`,
        firstHeader: 'Model',
        rows,
      };
    },

    async openDrilldown(session) {
      this.drilldown = { open: true, loading: true, deleting: false, records: [], session };
      try {
        const r = await fetch(`/api/sessions/${encodeURIComponent(session.session_id)}/records`);
        this.drilldown.records = await r.json() || [];
      } finally {
        this.drilldown.loading = false;
      }
    },
    closeDrilldown() {
      this.drilldown = { open: false, loading: false, deleting: false, records: [], session: null };
    },
    exportSession(session) {
      if (!session || !session.session_id) return;
      const a = document.createElement('a');
      const filename = this.sessionExportFilename(session);
      a.href = `/api/sessions/${encodeURIComponent(session.session_id)}/export?filename=${encodeURIComponent(filename)}`;
      a.download = filename;
      a.rel = 'noopener';
      document.body.appendChild(a);
      a.click();
      a.remove();
    },
    renderChart(bucket) {
      const ctx = document.getElementById('ts-chart');
      if (!ctx) return;
      // Destroy and rebuild — Chart.js's in-place update is finicky with
      // changing series lengths when the date filter shifts. Recreating is
      // a few ms and guarantees the canvas reflects the current filter.
      if (this.chart) { this.chart.destroy(); this.chart = null; }

      const fmtBucket = b => bucket === 'hour'
        ? new Date(b).toLocaleString([], { month:'short', day:'2-digit', hour:'2-digit' })
        : b.slice(0, 10);
      const labels = this.series.map(p => fmtBucket(p.bucket));
      const fmtNum = n => n.toLocaleString();
      const ds = (label, key, color) => ({ label, data: this.series.map(p => p[key]), backgroundColor: color, stack: 's' });
      const data = {
        labels,
        datasets: [
          ds('input',       'input_tokens',          '#58a6ff'),
          ds('output',      'output_tokens',         '#3fb950'),
          ds('cache write', 'cache_creation_tokens', '#d29922'),
          ds('cache read',  'cache_read_tokens',     '#8957e5'),
        ],
      };
      const series = this.series;
      const totalTokens = series.reduce((acc, p) =>
        acc + p.input_tokens + p.output_tokens + p.cache_creation_tokens + p.cache_read_tokens, 0);
      const totalCost = series.reduce((acc, p) => acc + (p.cost_usd || 0), 0);
      const fmtUSD = v => (v ?? 0).toLocaleString('en-US', { style:'currency', currency:'USD', minimumFractionDigits:2, maximumFractionDigits:2 });

      const self = this;
      const showAll = this.series.length <= 60;
      const opts = {
        responsive: true, maintainAspectRatio: false,
        animation: false,
        interaction: { mode: 'index', intersect: false },
        onHover: (event, _elements, chart) => {
          const hits = chart.getElementsAtEventForMode(event, 'index', { intersect: false }, false);
          chart.canvas.style.cursor = hits.length ? 'pointer' : 'default';
        },
        onClick: (event, _elements, chart) => {
          const hits = chart.getElementsAtEventForMode(event, 'index', { intersect: false }, false);
          if (!hits.length) return;
          self.openBucketDetail(hits[0].index);
        },
        scales: {
          x: { stacked: true, ticks:{ color:'#8b949e', autoSkip:true, maxTicksLimit: showAll ? 0 : 24 }, grid:{ color:'#21262d' } },
          y: { stacked: true, ticks:{ color:'#8b949e', callback: v => v >= 1e6 ? (v/1e6).toFixed(1)+'M' : v >= 1e3 ? (v/1e3).toFixed(1)+'k' : v }, grid:{ color:'#21262d' } },
        },
        plugins: {
          legend: { labels:{ color:'#e6edf3', usePointStyle: true, padding: 16 } },
          tooltip: {
            backgroundColor: '#161b22',
            titleColor: '#e6edf3',
            bodyColor: '#e6edf3',
            borderColor: '#30363d',
            borderWidth: 1,
            padding: 12,
            displayColors: true,
            callbacks: {
              title: (items) => {
                if (!items.length) return '';
                const i = items[0].dataIndex;
                const raw = series[i]?.bucket;
                if (!raw) return items[0].label;
                const d = new Date(raw);
                return bucket === 'hour'
                  ? d.toLocaleString([], { weekday:'short', month:'short', day:'2-digit', hour:'2-digit', minute:'2-digit' })
                  : d.toLocaleDateString([], { weekday:'long', year:'numeric', month:'long', day:'2-digit' });
              },
              label: (item) => {
                const v = item.parsed.y;
                return ` ${item.dataset.label.padEnd(12)} ${fmtNum(v)} tokens`;
              },
              afterBody: (items) => {
                if (!items.length) return [];
                const i = items[0].dataIndex;
                const p = series[i];
                if (!p) return [];
                const tot = p.input_tokens + p.output_tokens + p.cache_creation_tokens + p.cache_read_tokens;
                const inOut = p.input_tokens + p.output_tokens;
                const cache = p.cache_creation_tokens + p.cache_read_tokens;
                const cost = p.cost_usd || 0;
                const lines = [
                  '',
                  `Cost:           ${fmtUSD(cost)}`,
                  `Total tokens:   ${fmtNum(tot)}`,
                  `  in/out:       ${fmtNum(inOut)} (${tot > 0 ? (inOut/tot*100).toFixed(1) : 0}%)`,
                  `  cache r/w:    ${fmtNum(cache)} (${tot > 0 ? (cache/tot*100).toFixed(1) : 0}%)`,
                ];
                if (totalTokens > 0) {
                  lines.push(`Token share:    ${(tot / totalTokens * 100).toFixed(1)}% of period`);
                }
                if (totalCost > 0) {
                  lines.push(`Cost share:     ${(cost / totalCost * 100).toFixed(1)}% of period`);
                }
                return lines;
              },
            },
          },
        },
      };
      this.chart = new Chart(ctx, { type: 'bar', data, options: opts });
    },

    pricingSortValue(row) {
      const output = row.output_per_mtok_usd ?? 0;
      const input = row.input_per_mtok_usd ?? 0;
      return output * 1000000 + input;
    },
    pricingProviderName(model) {
      const m = String(model || '').toLowerCase();
      if (!m) return 'Other';
      if (m.includes('claude')) return 'Anthropic';
      if (m.includes('gpt') || m.includes('o1') || m.includes('o3') || m.includes('o4') || m.includes('omni') || m.includes('whisper') || m.includes('text-embedding') || m.includes('text-moderation')) return 'OpenAI';
      if (m.includes('gemini') || m.includes('gemma')) return 'Google';
      if (m.includes('llama')) return 'Meta';
      if (m.includes('mistral') || m.includes('mixtral')) return 'Mistral';
      if (m.includes('command') || m.includes('embed-english') || m.includes('embed-multilingual')) return 'Cohere';
      if (m.includes('grok')) return 'xAI';
      if (m.includes('deepseek')) return 'DeepSeek';
      if (m.includes('qwen')) return 'Alibaba';
      if (m.includes('kimi')) return 'Moonshot';
      if (m.includes('seed')) return 'ByteDance';
      if (m.includes('reka')) return 'Reka';
      return 'Other';
    },

    modelMetadataForModel(model) {
      const rows = (this.modelCatalog && this.modelCatalog.length ? this.modelCatalog : this.pricing) || [];
      const exact = rows.find(r => r.model === model);
      if (exact) return exact;
      let cur = String(model || '').replace(/-\d{8}$/, '');
      if (cur && cur !== model) {
        const dated = rows.find(r => r.model === cur);
        if (dated) return dated;
      }
      while (cur) {
        const next = cur.replace(/-\d+$/, '');
        if (next === cur || !next.includes('-')) break;
        cur = next;
        const match = rows.find(r => r.model === cur);
        if (match) return match;
      }
      return null;
    },
    sessionContextRows() {
      const records = this.drilldown?.records || [];
      const grouped = new Map();
      for (const r of records) {
        const key = r.model || '<unknown>';
        const total = (r.input_tokens || 0) + (r.output_tokens || 0) + (r.cache_creation_tokens || 0) + (r.cache_read_tokens || 0);
        const row = grouped.get(key) || {
          model: key,
          company: this.pricingProviderName(key),
          context_length: 0,
          max_output_tokens: 0,
          messages: 0,
          peak_prompt_tokens: 0,
          peak_output_tokens: 0,
          peak_total_tokens: 0,
          occupancy_pct: 0,
        };
        row.messages += 1;
        const prompt = (r.input_tokens || 0) + (r.cache_creation_tokens || 0) + (r.cache_read_tokens || 0);
        row.peak_prompt_tokens = Math.max(row.peak_prompt_tokens, prompt);
        row.peak_output_tokens = Math.max(row.peak_output_tokens, r.output_tokens || 0);
        row.peak_total_tokens = Math.max(row.peak_total_tokens, total);
        const meta = this.modelMetadataForModel(key);
        if (meta) {
          row.context_length = meta.context_length || 0;
          row.max_output_tokens = meta.max_output_tokens || 0;
        }
        row.occupancy_pct = row.context_length > 0 ? (row.peak_prompt_tokens / row.context_length) * 100 : 0;
        grouped.set(key, row);
      }
      return [...grouped.values()].sort((a, b) => {
        if (a.occupancy_pct !== b.occupancy_pct) return b.occupancy_pct - a.occupancy_pct;
        if (a.peak_prompt_tokens !== b.peak_prompt_tokens) return b.peak_prompt_tokens - a.peak_prompt_tokens;
        return String(a.model).localeCompare(String(b.model));
      });
    },
    sessionContextSummary() {
      const rows = this.sessionContextRows();
      const summary = {
        model_count: rows.length,
        peak_prompt_tokens: 0,
        peak_total_tokens: 0,
        largest_context_length: 0,
        highest_occupancy_pct: 0,
      };
      for (const row of rows) {
        summary.peak_prompt_tokens = Math.max(summary.peak_prompt_tokens, row.peak_prompt_tokens || 0);
        summary.peak_total_tokens = Math.max(summary.peak_total_tokens, row.peak_total_tokens || 0);
        summary.largest_context_length = Math.max(summary.largest_context_length, row.context_length || 0);
        summary.highest_occupancy_pct = Math.max(summary.highest_occupancy_pct, row.occupancy_pct || 0);
      }
      return summary;
    },
    sortedPricing() {
      const dir = this.pricingSort === 'price_asc' ? 1 : -1;
      return [...(this.pricing || [])].sort((a, b) => {
        const av = this.pricingSortValue(a);
        const bv = this.pricingSortValue(b);
        if (av !== bv) return (av - bv) * dir;
        return String(a.model || '').localeCompare(String(b.model || '')) * dir;
      });
    },
    pricingSources() {
      return [...new Set((this.pricing || []).map(p => p.source).filter(Boolean))].sort();
    },
    pricingProviders() {
      return [...new Set((this.pricing || []).map(p => this.pricingProviderName(p.model)).filter(Boolean))].sort();
    },
    filteredPricing() {
      const needle = (this.pricingSearch || '').trim().toLowerCase();
      const source = (this.pricingSource || '').trim().toLowerCase();
      const provider = (this.pricingProvider || '').trim().toLowerCase();
      let rows = this.sortedPricing();
      if (source) {
        rows = rows.filter(p => (p.source || '').toLowerCase() === source);
      }
      if (provider) {
        rows = rows.filter(p => this.pricingProviderName(p.model).toLowerCase() === provider);
      }
      if (!needle) return rows;
      return rows.filter(p => (p.model || '').toLowerCase().includes(needle));
    },
    pagedPricing() {
      const start = (this.pricingPage - 1) * this.pricingLimit;
      return this.filteredPricing().slice(start, start + this.pricingLimit);
    },
    get pricingPages() {
      return Math.max(1, Math.ceil(this.filteredPricing().length / this.pricingLimit));
    },
    pricingPageWindow() {
      const total = this.pricingPages, cur = this.pricingPage, span = 2;
      const start = Math.max(1, cur - span);
      const end = Math.min(total, cur + span);
      const out = [];
      for (let i = start; i <= end; i++) out.push(i);
      return out;
    },
    goPricingPage(n) {
      n = Math.max(1, Math.min(this.pricingPages, n));
      if (n === this.pricingPage) return;
      this.pricingPage = n;
    },
    pricingSearchChanged() {
      this.pricingPage = 1;
    },
    pricingSourceChanged() {
      this.pricingPage = 1;
    },
    pricingProviderChanged() {
      this.pricingPage = 1;
    },
    pricingSortChanged() {
      this.pricingPage = 1;
    },
    normalizePricingPage() {
      this.pricingPage = Math.max(1, Math.min(this.pricingPage, this.pricingPages));
    },

    async loadPricing() {
      const [rates, status] = await Promise.all([
        fetch('/api/pricing').then(r=>r.json()),
        fetch('/api/pricing/status').then(r=>r.json()).catch(() => null),
      ]);
      // Preserve per-row save state across reloads (same pattern as budgets).
      const prev = new Map((this.pricing || []).map(p => [p.model, p]));
      this.pricing = (rates || []).map(p => {
        const old = prev.get(p.model);
        return { ...p, _saving: false, _saveState: null, _resetting: false };
      });
      this.pricingStatus = status;
      this.normalizePricingPage();
    },
    pricingStatusText() {
      const s = this.pricingStatus;
      if (!s || s.provider === 'none') return '';
      if (s.last_error) return `Provider: ${s.provider} · last error: ${s.last_error}`;
      if (!s.last_sync_at) return `Provider: ${s.provider} · not synced yet`;
      const age = new Date(s.last_sync_at).toLocaleString();
      return `Provider: ${s.provider} · last sync: ${age} (${s.last_count} rows)`;
    },
    async syncPricing() {
      this.syncing = true;
      this.syncState = null;
      let ok = false;
      try {
        const r = await fetch('/api/pricing/sync', { method: 'POST' });
        ok = r.ok;
        if (!ok) {
          const txt = await r.text();
          console.error('OpenRouter sync failed:', txt);
        }
      } catch (e) {
        console.error(e);
      } finally {
        this.syncing = false;
        this.flashState('syncState', ok ? 'success' : 'error');
        await this.loadPricing();
        await this.loadModelCatalog();
        await this.reload();
      }
    },
    async savePricing(p) {
      // PUT is the in-place edit path — server preserves source so a tweaked
      // openrouter row still gets refreshed by the next sync; manual rows
      // stay manual.
      p._saving = true; p._saveState = null;
      let ok = false;
      try {
        const r = await fetch(`/api/pricing/${encodeURIComponent(p.model)}`, {
          method:'PUT',
          headers:{'Content-Type':'application/json'},
          body: JSON.stringify(p),
        });
        ok = r.ok;
      } catch (_) { ok = false; }
      finally {
        p._saving = false;
        p._saveState = ok ? 'success' : 'error';
        await this.loadPricing();
        await this.loadModelCatalog();
        await this.reload();
        const modelId = p.model;
        setTimeout(() => {
          const live = this.pricing.find(x => x.model === modelId);
          if (live) live._saveState = null;
        }, 2500);
      }
    },
    async addPricing() {
      const np = this.newPricing;
      const id = (np.model || '').trim();
      if (!id) return;

      // Client-side guard: catch obvious duplicates before hitting the server.
      // The server enforces the same rule (returns 409) — this is defense in
      // depth and gives the user a styled modal instead of a thrown fetch.
      const dup = (this.pricing || []).find(p => p.model === id);
      if (dup) {
        this.askConfirm(
          'Model already exists',
          `A pricing row for "${id}" already exists (source: ${dup.source}). Edit that row directly instead of creating a duplicate.`,
          () => {},
          'OK',
        );
        return;
      }

      const payload = { ...np, model: id };
      const r = await fetch('/api/pricing', {
        method:'POST',
        headers:{'Content-Type':'application/json'},
        body: JSON.stringify(payload),
      });
      if (r.status === 409) {
        // Race or stale UI: model was created elsewhere between page load and now.
        this.askConfirm('Model already exists',
          await r.text() || `A pricing row for "${id}" already exists.`,
          () => {}, 'OK');
        await this.loadPricing();
        await this.loadModelCatalog();
        return;
      }
      if (!r.ok) {
        this.askConfirm('Could not save', await r.text() || 'Server rejected the request.',
          () => {}, 'OK');
        return;
      }
      this.newPricing = { model: '', input_per_mtok_usd: 0, output_per_mtok_usd: 0, cache_write_per_mtok_usd: 0, cache_read_per_mtok_usd: 0 };
      await this.loadPricing();
      await this.loadModelCatalog();
      await this.reload();
    },
    deletePricing(p) {
      this.askConfirm(
        'Delete pricing?',
        `Remove pricing for "${p.model}". If this row was managed by OpenRouter it will reappear on the next sync — manual rows are gone for good.`,
        async () => {
          await fetch(`/api/pricing/${encodeURIComponent(p.model)}`, { method:'DELETE' });
          // Sort returns a shallow copy, so the row's index in the visible
          // table (`i` from x-for) doesn't match `this.pricing`. Find by id.
          const idx = this.pricing.findIndex(x => x.model === p.model);
          if (idx >= 0) this.pricing.splice(idx, 1);
          this.normalizePricingPage();
          await this.loadModelCatalog();
          await this.reload();
        },
      );
    },

    factoryResetPricing(p) {
      this.askConfirm(
        'Restore factory pricing?',
        `Reset "${p.model}" to the factory pricing source. This removes manual edits and restores the official values when available. Models without a factory source are kept unchanged.`,
        async () => {
          p._resetting = true;
          try {
            const r = await fetch(`/api/pricing/${encodeURIComponent(p.model)}/factory-reset`, { method:'POST' });
            if (!r.ok) {
              this.askConfirm('Could not restore factory pricing', await r.text() || 'Server rejected the request.', () => {}, 'OK');
              return;
            }
            const res = await r.json().catch(() => null);
            if (res && res.action === 'skipped') {
              this.askConfirm('No factory source available', `"${p.model}" does not have a factory pricing source configured, so the current row was kept as-is.`, () => {}, 'OK');
            }
            await this.loadPricing();
            await this.loadModelCatalog();
            await this.reload();
          } finally {
            p._resetting = false;
          }
        },
        'Restore',
      );
    },
    factoryResetAllPricing() {
      this.askConfirm(
        'Restore factory pricing for all models?',
        'This forces every pricing row back to the factory source and removes manual edits where a factory source exists. Models without a factory source are kept unchanged.',
        async () => {
          this.syncing = true;
          this.syncState = null;
          try {
            const r = await fetch('/api/pricing/factory-reset', { method:'POST' });
            if (!r.ok) {
              this.askConfirm('Could not restore factory pricing', await r.text() || 'Server rejected the request.', () => {}, 'OK');
              this.flashState('syncState', 'error');
              return;
            }
            const res = await r.json().catch(() => null);
            if (res && res.deleted > 0) {
              this.askConfirm('Factory restore completed with removals', `${res.deleted} row(s) were removed during factory restore.`, () => {}, 'OK');
            }
            this.flashState('syncState', 'success');
            await this.loadPricing();
            await this.loadModelCatalog();
            await this.reload();
          } finally {
            this.syncing = false;
          }
        },
        'Restore all',
      );
    },

    async loadRTK() {
      // Full reload: fetches summary, unfiltered timeseries, and commands list.
      // Clears any active command filter.
      this.rtkCommandFilter = null;
      const qs = this.rtkQS();
      const withQS = base => qs ? `${base}${base.includes('?') ? '&' : '?'}${qs}` : base;
      const [summary, timeseries, commands] = await Promise.all([
        fetch(withQS('/api/rtk/summary')).then(r=>r.json()).catch(() => ({})),
        fetch(withQS('/api/rtk/timeseries?bucket=day')).then(r=>r.json()).catch(() => []),
        fetch(withQS('/api/rtk/commands')).then(r=>r.json()).catch(() => []),
      ]);
      this.rtkSummary = summary;
      this.rtkCommands = commands || [];

      // Trend from unfiltered data before overwriting rtkTimeseries.
      const ts = timeseries || [];
      const recent = ts.slice(-7);
      const prev   = ts.slice(-14, -7);
      const sum7 = (arr, k) => arr.reduce((a, p) => a + (p[k] || 0), 0);
      const pct  = (r, p) => p > 0 ? (r - p) / p * 100 : null;
      this.rtkTrend = {
        savedTokens: pct(sum7(recent, 'saved_tokens'), sum7(prev, 'saved_tokens')),
        commands:    pct(sum7(recent, 'commands'),     sum7(prev, 'commands')),
        timeMs:      pct(sum7(recent, 'total_time_ms'), sum7(prev, 'total_time_ms')),
      };

      this.rtkTimeseries = ts;
      await this.$nextTick();
      this.renderRTKChart();
    },

    async filterRTKCommand(cmd) {
      // Toggle: clicking the active filter clears it.
      this.rtkCommandFilter = this.rtkCommandFilter === cmd ? null : cmd;

      const qs = this.rtkQS();
      const base = this.rtkCommandFilter
        ? `/api/rtk/timeseries?bucket=day&command=${encodeURIComponent(this.rtkCommandFilter)}`
        : '/api/rtk/timeseries?bucket=day';
      const url = qs ? `${base}&${qs}` : base;

      // Only re-fetch timeseries — summary and commands don't change per-command.
      const timeseries = await fetch(url).then(r=>r.json()).catch(() => []);
      this.rtkTimeseries = timeseries || [];
      await this.$nextTick();
      this.renderRTKChart();
    },

    openRTKCard(type) {
      const ts = this.rtkTimeseries;
      let title, rows;
      if (type === 'saved') {
        const total = ts.reduce((a, p) => a + (p.saved_tokens || 0), 0) || 1;
        title = `Tokens saved: ${this.fmtTokens(this.rtkSummary?.saved_tokens || 0)}`;
        rows = [...ts].sort((a, b) => b.saved_tokens - a.saved_tokens).map(p => ({
          label: p.date,
          value: p.saved_tokens,
          formatted: this.fmtTokens(p.saved_tokens || 0),
          pct: (p.saved_tokens || 0) / total * 100,
        }));
      } else if (type === 'commands') {
        const total = ts.reduce((a, p) => a + (p.commands || 0), 0) || 1;
        title = `Commands intercepted: ${(this.rtkSummary?.total_commands || 0).toLocaleString()}`;
        rows = [...ts].sort((a, b) => b.commands - a.commands).map(p => ({
          label: p.date,
          value: p.commands,
          formatted: (p.commands || 0).toLocaleString(),
          pct: (p.commands || 0) / total * 100,
        }));
      } else if (type === 'time') {
        const total = ts.reduce((a, p) => a + (p.total_time_ms || 0), 0) || 1;
        const fmt = ms => ms >= 60000 ? `${(ms/60000).toFixed(1)}m` : ms >= 1000 ? `${(ms/1000).toFixed(1)}s` : `${ms}ms`;
        title = `Time saved: ${fmt(this.rtkSummary?.total_time_ms || 0)}`;
        rows = [...ts].sort((a, b) => b.total_time_ms - a.total_time_ms).map(p => ({
          label: p.date,
          value: p.total_time_ms,
          formatted: fmt(p.total_time_ms || 0),
          pct: (p.total_time_ms || 0) / total * 100,
        }));
      }
      this.rtkModal = { open: true, title, subtitle: 'Daily breakdown — sorted by highest value', firstHeader: 'Date', rows: rows || [] };
    },

    closeRTKModal() { this.rtkModal.open = false; },

    async openRTKDayDetail(index) {
      const p = this.rtkTimeseries[index];
      if (!p) return;
      const fmt = ms => ms >= 60000 ? `${(ms/60000).toFixed(1)}m` : ms >= 1000 ? `${(ms/1000).toFixed(1)}s` : `${ms}ms`;
      const cmds = await fetch(`/api/rtk/commands?date=${encodeURIComponent(p.date)}&limit=10`)
        .then(r => r.json()).catch(() => []);
      const totalSaved = cmds.reduce((a, c) => a + (c.saved_tokens || 0), 0) || 1;
      this.rtkModal = {
        open: true,
        title: `${p.date} — ${this.fmtTokens(p.saved_tokens || 0)} saved`,
        subtitle: `${p.commands} commands · ${(p.savings_pct || 0).toFixed(1)}% efficiency · ${fmt(p.total_time_ms || 0)}`,
        firstHeader: 'Command',
        rows: cmds.map(c => ({
          label: c.command.length > 50 ? c.command.slice(0, 47) + '...' : c.command,
          value: c.saved_tokens,
          formatted: this.fmtTokens(c.saved_tokens),
          pct: c.saved_tokens / totalSaved * 100,
        })),
      };
    },

    // For RTK: going up is GOOD (opposite of cost dashboard).
    rtkDeltaClass(pct) {
      if (pct === null || pct === undefined || !isFinite(pct)) return '';
      return pct > 0 ? 'rtk-up' : pct < 0 ? 'rtk-down' : '';
    },
    rtkDeltaText(pct) {
      if (pct === null || pct === undefined) return '';
      if (!isFinite(pct)) return 'new';
      const arrow = pct > 0 ? '▲' : pct < 0 ? '▼' : '·';
      return `${arrow} ${Math.abs(pct).toFixed(1)}%`;
    },

    fmtTokens(n) {
      if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
      if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
      return n.toString();
    },

    renderRTKChart() {
      // Always destroy first so a cleared filter doesn't leave a stale chart.
      try {
        if (this.rtkChart) { this.rtkChart.destroy(); }
      } catch (_) { /* canvas may have been replaced; ignore */ }
      this.rtkChart = null;

      if (typeof Chart === 'undefined' || !this.rtkTimeseries || this.rtkTimeseries.length === 0) return;

      const ctx = document.getElementById('rtkChart');
      // Bail if canvas is not visible — offsetParent is null for hidden elements.
      if (!ctx || ctx.offsetParent === null) return;

      // Close over data so Chart.js tooltip callbacks can access it without `this`.
      const tsData   = this.rtkTimeseries;
      const filtered = !!this.rtkCommandFilter;
      const label    = filtered
        ? `Tokens saved — ${this.rtkCommandFilter.length > 40 ? this.rtkCommandFilter.slice(0, 37) + '...' : this.rtkCommandFilter}`
        : 'Tokens saved';

      this.rtkChart = new Chart(ctx, {
        type: 'bar',
        data: {
          labels: tsData.map(p => p.date),
          datasets: [{
            label,
            data: tsData.map(p => p.saved_tokens || 0),
            backgroundColor: filtered ? 'rgba(74, 143, 207, 0.6)' : 'rgba(75, 192, 75, 0.6)',
            borderColor:     filtered ? 'rgba(74, 143, 207, 1)'   : 'rgba(75, 192, 75, 1)',
            borderWidth: 1,
          }],
        },
        options: {
          responsive: true,
          maintainAspectRatio: true,
          interaction: { mode: 'index', intersect: false },
          onHover: (event, _elements, chart) => {
            const hits = chart.getElementsAtEventForMode(event, 'index', { intersect: false }, false);
            chart.canvas.style.cursor = hits.length ? 'pointer' : 'default';
          },
          onClick: (event, _elements, chart) => {
            const hits = chart.getElementsAtEventForMode(event, 'index', { intersect: false }, false);
            if (!hits.length) return;
            this.openRTKDayDetail(hits[0].index);
          },
          scales: { y: { beginAtZero: true, title: { display: true, text: 'Tokens' } } },
          plugins: {
            legend: { display: true },
            tooltip: {
              callbacks: {
                afterLabel(c) {
                  const p = tsData[c.dataIndex];
                  return p ? `${(p.savings_pct || 0).toFixed(1)}% efficiency · click for detail` : '';
                },
              },
            },
          },
        },
      });
    },
  };
}
