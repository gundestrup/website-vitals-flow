// app.js — Alpine hydration only. All vitals cards / findings / console /
// runs markup is server-rendered from Go html/template partials; Alpine
// covers the interactive layer: scan actions, jobs live-status, trend
// charts, the pages dropdown, and lang/theme toggles.
const wvfI18n = {
  en:{monitored:'Monitored sites',recent:'Recent scans',adhoc:'(ad-hoc — not scheduled)',
    scanall:'scan all',deepscan:'deep scan',addsite:'add site',scanurl:'scan url',
    add:'add',scan:'scan',edit:'edit',prune:'prune',all:'← all sites',
    shot:'last scan screenshot',trends:'Trends',findings:'Findings',console:'Console',
    runs:'Runs',jobs:'jobs running',url_ph:'https://site or localhost:3000',sched_ph:'schedule 30m'},
  da:{monitored:'Overvågede sites',recent:'Seneste scanninger',adhoc:'(ad-hoc — ikke planlagt)',
    scanall:'scan alle',deepscan:'dyb scan',addsite:'tilføj site',scanurl:'scan url',
    add:'tilføj',scan:'scan',edit:'ret',prune:'rens',all:'← alle sites',
    shot:'seneste skærmbillede',trends:'Tendenser',findings:'Fund',console:'Konsol',
    runs:'Kørsler',jobs:'jobs kører',url_ph:'https://site eller localhost:3000',sched_ph:'planlæg 30m'},
};

// hdr is the shared lang/theme chrome — also the whole Alpine scope on
// read-only pages (report).
const hdrBase = () => ({
  lang: localStorage.getItem('wvf-lang') || 'en',
  theme: '',
  t(k) { return (wvfI18n[this.lang] && wvfI18n[this.lang][k]) || wvfI18n.en[k] || k },
  toggleLang() {
    this.lang = this.lang === 'en' ? 'da' : 'en';
    localStorage.setItem('wvf-lang', this.lang);
  },
  themeIcon() { return { auto: '◐ auto', light: '☀ light', dark: '☾ dark' }[this.theme || wvfTheme.get()] },
  cycleTheme() { this.theme = wvfTheme.cycle() },
});

document.addEventListener('alpine:init', () => {
  Alpine.data('hdr', hdrBase);
  Alpine.data('dash', () => Object.assign(hdrBase(), {
    sites: [], last: {}, jobs: [], runs: [], sel: '', busy: false,
    charts: {}, pages: [], page: '',
    addUrl: '', addSched: '', scanType: '', onlySel: [],
    onlyOpts: ['ttfb','fcp','lcp','cls','tbt','inp','vitals','http','h3','images','js','css','deep','psi'],
    band(id, v) {
      const b = { ttfb:[800,1800], fcp:[1800,3000], lcp:[2500,4000],
        cls:[0.1,0.25], tbt:[200,600], inp:[200,500] }[id];
      if (v == null || !b) return 'band-na';
      return v > b[1] ? 'band-poor' : v > b[0] ? 'band-ok' : 'band-good';
    },
    bandName(id, v) { return this.band(id, v).replace('band-', '') },
    fmtv(id, v) { return id === 'cls' ? (v == null ? '-' : v.toFixed(3)) : (v == null ? '-' : `${Math.round(v)}ms`) },
    // site-row metric cell: {band,text} for badge+value; null = no data
    mcell(s, m) { // always returns object — CSP parser can't do ||{}
      const r = this.last[s.key];
      if (!r || (m === 'cls' ? r.cls : r[`${m}_ms`]) == null) return { ok: false, band: 'na', text: '' };
      const v = m === 'cls' ? r.cls : r[`${m}_ms`];
      return { ok: true, band: this.bandName(m, v), text: m === 'cls' ? v.toFixed(3) : `${Math.round(v)}ms` };
    },
    monitored() { return this.sites.filter(s => s.schedule) },
    adhoc() { return this.sites.filter(s => !s.schedule) },
    siteByKey(k) { return this.sites.find(x => x.key === k) || { key: k } },
    // encodeURIComponent lives in a method — the CSP evaluator can't
    // reach globals from attribute expressions
    siteHref(k) { return `/site/${encodeURIComponent(k)}` },
    async j(u, o) { const r = await fetch(u, o); return r.json() },
    // front page: sites + last-run badges + live jobs
    async load() {
      this.sites = await this.j('/api/sites') || [];
      this.jobs = await this.j('/api/jobs') || [];
      for (const s of this.sites) {
        const rr = await this.j(`/api/sites/${encodeURIComponent(s.key)}/runs`);
        // last successful run — failed ticks carry null metrics
        if (rr?.length) this.last[s.key] = rr.find(r => r.status === 'ok') || rr[0];
      }
      this.startSSE();
      const active = this.jobs.some(j => j.state === 'queued' || j.state === 'running');
      clearTimeout(this._poll);
      this._poll = setTimeout(() => this.load(), active ? 5000 : 15000);
    },
    // site detail: key comes from the path — server already rendered
    // cards/findings/console/runs; Alpine owns charts + actions + jobs.
    async initSite() {
      const m = location.pathname.match(/^\/site\/(.+)/);
      this.sel = m ? decodeURIComponent(m[1]) : '';
      this.sites = await this.j('/api/sites') || [];
      this.jobs = await this.j('/api/jobs') || [];
      this.runs = await this.j(`/api/sites/${encodeURIComponent(this.sel)}/runs`) || [];
      this.loadPages();
      this.startSSE();
      setTimeout(() => this.draw(), 0);
    },
    // SSE pushes jobs live; a job reaching a terminal state refreshes
    // the page (reload on detail so the server re-renders the cards).
    startSSE() {
      if (this._sse) return;
      this._sse = new EventSource('/api/jobs/stream');
      this._sse.onmessage = (e) => {
        const next = JSON.parse(e.data);
        const done = (next || []).filter(j => j.state === 'done' || j.state === 'failed')
          .map(j => j.id);
        const wasDone = this.jobs.filter(j => j.state === 'done' || j.state === 'failed')
          .map(j => j.id);
        this.jobs = next || [];
        if (done.some(i => !wasDone.includes(i))) this.refresh();
      };
    },
    refresh() { if (this.sel) location.reload(); else this.load(); },
    // detail-page actions bound to the server-rendered key
    scanSel() { this.scanSite(this.siteByKey(this.sel)) },
    editSel() { this.editSite(this.siteByKey(this.sel)) },
    delSel() { this.delSite(this.siteByKey(this.sel)) },
    activeJob() {
      const site = this.siteByKey(this.sel);
      return this.jobs.find(j => (j.state === 'queued' || j.state === 'running') && j.site_id === site.id);
    },
    jobText() { const j = this.activeJob(); return j ? `job ${j.id}: ${j.state} ${j.detail || ''}` : '' },
    // add/edit/delete monitored sites via the API
    async addSite() {
      if (!this.addUrl) return;
      const only = this.scanType === 'custom' ? this.onlySel.join(',') : (this.scanType || '');
      await this.j('/api/sites', { method: 'POST', body: JSON.stringify({
        url: this.addUrl, schedule: this.addSched, deep: this.scanType === 'deep', only }) });
      this.addUrl = ''; this.addSched = ''; await this.load();
    },
    async editSite(s) {
      const schedule = prompt('schedule (30m, 6h, daily — empty = manual):', s.schedule || '');
      if (schedule === null) return;
      const only = prompt('only set (empty = all):', s.only || '');
      if (only === null) return;
      const deep = confirm('run deep scans for this site? (attribution + lighthouse)');
      await this.j(`/api/sites/${s.key}`, { method: 'PATCH',
        body: JSON.stringify({ schedule, only, deep }) });
      this.refresh();
    },
    async delSite(s) {
      if (!confirm(`remove ${s.key} and its history?`)) return;
      await this.j(`/api/sites/${s.key}`, { method: 'DELETE' });
      if (this.sel === s.key) { location.href = '/'; return }
      await this.load();
    },
    async scanAll(deep) {
      this.busy = true;
      await this.j('/api/scan-all', { method: 'POST', body: JSON.stringify({ deep }) });
      setTimeout(async () => { this.busy = false; await this.load(); }, 2500);
    },
    async loadPages() {
      this.pages = await this.j(`/api/sites/${encodeURIComponent(this.sel)}/pages`) || ['/'];
    },
    async scanPage() {
      if (!this.page) return;
      this.busy = true;
      await this.j('/api/scan-now', { method: 'POST',
        body: JSON.stringify({ key: this.sel, page: this.page }) });
      setTimeout(() => { this.busy = false; }, 2000);
    },
    async scanSite(s) {
      this.busy = true;
      const only = this.scanType === 'custom' ? this.onlySel.join(',') : (this.scanType === 'deep' ? '' : this.scanType);
      await this.j('/api/scan-now', { method: 'POST', body: JSON.stringify({
        key: s.key, deep: this.scanType === 'deep', only: only || '' }) });
      setTimeout(() => { this.busy = false; this.refresh(); }, 2000);
    },
    // ad-hoc url scan via POST /api/scan
    async scanUrl() {
      if (!this.addUrl) return;
      this.busy = true;
      const only = this.scanType === 'custom' ? this.onlySel.join(',') : '';
      const r = await this.j('/api/scan', { method: 'POST', body: JSON.stringify({
        url: this.addUrl, wait: 60, deep: this.scanType === 'deep', only }) });
      if (!r || r.error) { this.busy = false;
        alert(`scan failed: ${(r?.error) || 'request failed'}`); return }
      this.busy = false; this.addUrl = ''; await this.load();
    },
    async prune(key) {
      const r = await this.j('/api/prune', { method: 'POST', body: JSON.stringify({ key }) });
      alert(`pruned ${r?.dropped} runs per retention policy`);
    },
    draw() {
      const rs = [...this.runs].reverse();
      const labels = rs.map(r => r.started_at.slice(5, 16));
      const KB = b => b ? Math.round(b / 1024) : null;
      const specs = [
        { id: 'ttfb', label: 'TTFB ms', v: r => r.ttfb_ms, b: [800,1800], d: 'time to first byte — server response speed' },
        { id: 'fcp', label: 'FCP ms', v: r => r.fcp_ms, b: [1800,3000], d: 'first contentful paint — first visible content' },
        { id: 'lcp', label: 'LCP ms', v: r => r.lcp_ms, b: [2500,4000], d: 'largest contentful paint — main content ready' },
        { id: 'cls', label: 'CLS', v: r => r.cls, b: [0.1,0.25], d: 'cumulative layout shift — visual stability' },
        { id: 'tbt', label: 'TBT ms', v: r => r.tbt_ms, b: [200,600], d: 'total blocking time — main-thread busy' },
        { id: 'reqs', label: 'requests', v: r => r.request_count, b: [75,150], d: 'number of network requests per load' },
        { id: 'weight', label: 'weight KB', v: r => KB(r.page_bytes), b: [1536,3072], d: 'total transferred page weight' },
      ];
      const bandColor = { good: '#2e7d32', ok: '#f9a825', poor: '#c62828', na: '#97a5a0' };
      for (const sp of specs) {
        const el = document.getElementById(`tr-${sp.id}`); if (!el) continue;
        if (this.charts[sp.id]) this.charts[sp.id].destroy();
        const vals = rs.map(sp.v);
        // point colors by band — green/amber/red like LH score rings
        const ptColor = vals.map(v => {
          if (v == null || !sp.b) return bandColor.na;
          return bandColor[v > sp.b[1] ? 'poor' : v > sp.b[0] ? 'ok' : 'good'];
        });
        const ds = [{ label: sp.label, data: vals,
          backgroundColor: 'hsl(210 39% 44% / .12)',
          pointBackgroundColor: ptColor, pointBorderColor: ptColor, pointRadius: 3,
          // segment colored by destination point's band — the line
          // itself goes green/yellow/red like LH score bands
          segment: { borderColor: c => {
            const v = vals[c.p1DataIndex];
            if (v == null || !sp.b) return bandColor.na;
            return bandColor[v > sp.b[1] ? 'poor' : v > sp.b[0] ? 'ok' : 'good'];
          } },
          tension: .3, fill: true }];
        if (sp.b) { ds.push(
          // amber zone: fill between the two cutoff values
          { data: labels.map(() => sp.b[0]), borderWidth: 0, pointRadius: 0, fill: false },
          { data: labels.map(() => sp.b[1]), borderWidth: 0, pointRadius: 0,
            fill: '-1', backgroundColor: 'rgba(249,165,37,.14)' },
          // boundary dashes
          { data: labels.map(() => sp.b[0]), borderColor: '#2e7d32', borderDash: [5,4], pointRadius: 0, borderWidth: 1.4 },
          { data: labels.map(() => sp.b[1]), borderColor: '#c62828', borderDash: [5,4], pointRadius: 0, borderWidth: 1.4 }); }
        this.charts[sp.id] = new Chart(el, { type: 'line',
          data: { labels, datasets: ds },
          options: { plugins: { legend: { display: false },
            tooltip: { callbacks: { footer: () => sp.d } } },
            maintainAspectRatio: false,
            scales: { x: { display: false }, y: { ticks: { font: { size: 9 } } } } } });
      }
    },
  }));
});
