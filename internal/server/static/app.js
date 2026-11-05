document.addEventListener('alpine:init', () => {
  Alpine.data('dash', () => ({
    sites: [], last: {}, jobs: [], runs: [], sel: '', busy: false, chart: null, findings: [],
    fmt(v){ return v==null?'-':Math.round(v) },
    async j(u,o){ const r=await fetch(u,o); return r.json() },
    async load(){
      this.sites = await this.j('/api/sites') || [];
      const h=location.hash.slice(1) || new URLSearchParams(location.search).get('site');
      if(h && !this.sel){ this.sel=h; this.open(h); }
      this.jobs = await this.j('/api/jobs') || [];
      for (const s of this.sites){
        const rr = await this.j('/api/sites/'+s.key+'/runs');
        if (rr && rr.length) this.last[s.key]=rr[0];
      }
    },
    async open(key){
      this.sel=key;
      this.runs=(await this.j('/api/sites/'+key+'/runs'))||[];
      if(this.runs.length){
        const det=await this.j('/api/runs/'+this.runs[0].id);
        try{ this.findings=JSON.parse(det.detail_json).findings||[] }catch(e){ this.findings=[] }
      }
      this.draw();
    },
    async scan(key){
      this.busy=true;
      await this.j('/api/scan-now',{method:'POST',body:JSON.stringify({key})});
      setTimeout(async()=>{ this.busy=false; await this.load(); if(this.sel==key) this.open(key); },2000);
    },
    draw(){
      const rs=[...this.runs].reverse();
      const per={};
      for(const e of ['cdp','lighthouse','psi']){
        per[e]=rs.filter(r=>r.engine===e);
      }
      const ds=[];
      for(const e of Object.keys(per)){
        if(!per[e].length) continue;
        ds.push({label:e+' TTFB',data:per[e].map(r=>r.ttfb_ms),borderColor:'#888',tension:.3,hidden:true});
        ds.push({label:e+' LCP',data:per[e].map(r=>r.lcp_ms),tension:.3});
        ds.push({label:e+' FCP',data:per[e].map(r=>r.fcp_ms),borderDash:[4,4],tension:.3,hidden:true});
      }
      const labels=rs.map(r=>r.started_at.slice(5,16));
      if(this.chart) this.chart.destroy();
      this.chart=new Chart(document.getElementById('chart'),{type:'line',
        data:{labels,datasets:ds},
        options:{plugins:{legend:{display:true}},scales:{y:{title:{text:'ms',display:true}}}}});
    }
  }));
});
