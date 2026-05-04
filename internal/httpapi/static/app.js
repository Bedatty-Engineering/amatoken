function amatoken() {
  return {
    tab: 'dashboard',
    // Per-tab filters: Dashboard and Sessions keep independent state so
    // changing one never affects the other.
    filters: {
      dashboard: { range: 'all', from: '', to: '', project: '', model: '' },
      sessions:  { range: 'all', from: '', to: '', project: '', model: '', search: '' },
    },
    options: { projects: [], models: [] },
    summary: {},
    series: [],
    sessions: [],
    total: 0,
    page: 1,
    limit: 50,
    rankings: { projects: [], models: [] },
    pricing: [],
    pricingStatus: null,
    syncing: false,
    refreshing: false,
    refreshState: null,    // null | 'success' | 'error'
    syncState: null,       // null | 'success' | 'error'
    comparison: null,      // { cost_usd, sessions, messages, input_tokens, ... } as % delta
    comparisonLabel: '',   // human label like "vs previous 7 days" or "vs last month"
    budgets: [],
    newBudget: { name: '', amount_usd: 0 },
    drilldown: { open: false, loading: false, records: [], session: null },
    metricDetail: { open: false, title: '', subtitle: '', firstHeader: '', rows: [] },
    confirmModal: { open: false, title: '', message: '', confirmLabel: 'Delete', onConfirm: null },
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

    async init() {
      await this.loadFilterOptions();
      await this.loadBudgets();
      await this.loadAutomationSettings();
      await this.reload();
      await this.loadPricing();
      await this.loadRTK();
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
      this.filters.dashboard = { range: 'all', from: '', to: '', project: '', model: '' };
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
      return p.toString();
    },
    searchChanged() {
      this.page = 1; // search shrinks the result set; jump back to page 1.
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
      this.drilldown = { open: true, loading: true, records: [], session };
      try {
        const r = await fetch(`/api/sessions/${encodeURIComponent(session.session_id)}/records`);
        this.drilldown.records = await r.json() || [];
      } finally {
        this.drilldown.loading = false;
      }
    },
    closeDrilldown() {
      this.drilldown = { open: false, loading: false, records: [], session: null };
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
        onHover: (event, elements, chart) => {
          chart.canvas.style.cursor = elements.length ? 'pointer' : 'default';
        },
        onClick: (_event, elements) => {
          if (!elements.length) return;
          self.openBucketDetail(elements[0].index);
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

    // Pricing rows ordered most-expensive first. Output rate is the
    // primary key (it's the cost driver — ~5× input), with input rate as
    // tiebreaker so models with the same output but different input still
    // get a stable order. Returns a shallow copy so Save state references
    // (the live `pricing` array) aren't disturbed.
    sortedPricing() {
      return [...(this.pricing || [])].sort((a, b) => {
        const ao = a.output_per_mtok_usd ?? 0;
        const bo = b.output_per_mtok_usd ?? 0;
        if (bo !== ao) return bo - ao;
        const ai = a.input_per_mtok_usd ?? 0;
        const bi = b.input_per_mtok_usd ?? 0;
        return bi - ai;
      });
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
        return { ...p, _saving: old?._saving || false, _saveState: old?._saveState || null };
      });
      this.pricingStatus = status;
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
        return;
      }
      if (!r.ok) {
        this.askConfirm('Could not save', await r.text() || 'Server rejected the request.',
          () => {}, 'OK');
        return;
      }
      this.newPricing = { model: '', input_per_mtok_usd: 0, output_per_mtok_usd: 0, cache_write_per_mtok_usd: 0, cache_read_per_mtok_usd: 0 };
      await this.loadPricing();
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
          await this.reload();
        },
      );
    },

    async loadRTK() {
      const [summary, timeseries] = await Promise.all([
        fetch('/api/rtk/summary').then(r=>r.json()).catch(() => ({})),
        fetch('/api/rtk/timeseries?bucket=day').then(r=>r.json()).catch(() => []),
      ]);
      this.rtkSummary = summary;
      this.rtkTimeseries = timeseries || [];
      this.renderRTKChart();
    },

    renderRTKChart() {
      if (typeof Chart === 'undefined' || !this.rtkTimeseries || this.rtkTimeseries.length === 0) return;

      const ctx = document.getElementById('rtkChart');
      if (!ctx) return;

      if (this.rtkChart) this.rtkChart.destroy();

      this.rtkChart = new Chart(ctx, {
        type: 'bar',
        data: {
          labels: this.rtkTimeseries.map(p => p.date),
          datasets: [
            {
              label: 'Tokens saved',
              data: this.rtkTimeseries.map(p => p.saved_tokens || 0),
              backgroundColor: 'rgba(75, 192, 75, 0.6)',
              borderColor: 'rgba(75, 192, 75, 1)',
              borderWidth: 1,
            },
          ],
        },
        options: {
          responsive: true,
          maintainAspectRatio: true,
          scales: {
            y: { beginAtZero: true, title: { display: true, text: 'Tokens' } },
          },
          plugins: {
            legend: { display: true },
            tooltip: {
              callbacks: {
                afterLabel(ctx) {
                  const idx = ctx.dataIndex;
                  const p = this.$scope.rtkTimeseries[idx];
                  return p ? `${(p.savings_pct || 0).toFixed(1)}% saved` : '';
                },
              },
            },
          },
        },
      });
    },
  };
}
