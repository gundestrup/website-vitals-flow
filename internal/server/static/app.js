document.addEventListener('alpine:init', () => {
  Alpine.data('dash', () => ({
    sites: [], last: {}, jobs: [], runs: [], sel: '', busy: false, chart: null, findings: [],
    fmt(v){ return v==null?'-':Math.round(v) },
    band(id,v){
      const b={ttfb:[800,1800],fcp:[1800,3000],lcp:[2500,4000],
               cls:[0.1,0.25],tbt:[200,600],inp:[200,500]}[id];
      if(v==null||!b) return 'band-na';
      return v>b[1]?'band-poor':v>b[0]?'band-ok':'band-good';
    },
    bandName(id,v){
      return this.band(id,v).replace('band-','');
    },
    fmtv(id,v){ return id==='cls'?(v==null?'-':v.toFixed(3)):(v==null?'-':Math.round(v)+'ms') },
    chart2: null,
    metricCards(){
      const r=this.last[this.sel]; if(!r) return [];
      const out=[];
      const ms=(id,v,label)=>{out.push({id,band:this.bandName(id,v),
        text:this.fmtv(id,v),label})};
      ms('ttfb',r.ttfb_ms,'TTFB'); ms('fcp',r.fcp_ms,'FCP');
      ms('lcp',r.lcp_ms,'LCP');   ms('cls',r.cls,'CLS');
      ms('tbt',r.tbt_ms,'TBT');
      out.push({id:'',band:'na',text:r.request_count??'-',label:'requests'});
      out.push({id:'',band:'na',text:r.page_bytes?Math.round(r.page_bytes/1024)+'KB':'-',label:'weight'});
      out.push({id:'',band:'na',text:r.dom_nodes??'-',label:'DOM nodes'});
      const proto=r.http_protocol||'';
      out.push({id:'proto',band:proto.startsWith('h3')||proto.startsWith('h2')?'good':proto?'ok':'na',
        text:proto||'-',label:'protocol'});
      const enc=r.content_encoding||'';
      out.push({id:'comp',band:(enc==='br'||enc==='zstd')?'good':enc==='gzip'?'ok':enc?'poor':'na',
        text:enc||'-',label:'encoding'});
      const tls=r.tls_version||'';
      out.push({id:'tls',band:tls.includes('1.3')?'good':tls?'ok':'na',
        text:tls||'-',label:'TLS'});
      return out;
    },
    async j(u,o){ const r=await fetch(u,o); return r.json() },
    async load(){
      this.sites = await this.j('/api/sites') || [];
      const h=location.hash.slice(1) || new URLSearchParams(location.search).get('site');
      if(h && !this.sel){ this.sel=h; this.open(h); }
      this.jobs = await this.j('/api/jobs') || [];
      for (const s of this.sites){
        const rr = await this.j('/api/sites/'+s.key+'/runs');
        // last successful run — failed ticks carry null metrics
        if (rr && rr.length) this.last[s.key]=rr.find(r=>r.status==='ok')||rr[0];
      }
    },
    async open(key){
      this.sel=key; location.hash=key;
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
      // second chart: CLS + request count (separate scale from ms)
      const c2=document.getElementById('chart2');
      if(c2){
        if(this.chart2) this.chart2.destroy();
        this.chart2=new Chart(c2,{type:'line',
          data:{labels,datasets:[
            {label:'CLS',data:rs.map(r=>r.cls),tension:.3},
            {label:'requests',data:rs.map(r=>r.request_count),tension:.3,yAxisID:'y1'}]},
          options:{scales:{y:{title:{text:'CLS',display:true}},
            y1:{position:'right',title:{text:'reqs',display:true},grid:{drawOnChartArea:false}}}}});
      }
    }
  }));
});
