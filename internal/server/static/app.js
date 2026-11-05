document.addEventListener('alpine:init', () => {
  Alpine.data('dash', () => ({
    sites: [], last: {}, jobs: [], runs: [], sel: '', busy: false, chart: null, findings: [],
    addUrl:'', addSched:'', scanType:'', onlySel:[],
    onlyOpts:['ttfb','fcp','lcp','cls','tbt','inp','vitals','http','h3','images','js','css','deep','psi'],
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
    det: null, charts: {},
    age(ts){
      const ms=Date.now()-new Date(ts).getTime();
      const m=ms/6e4; if(m<60) return Math.max(1,Math.round(m))+'m';
      const h=m/60;  if(h<24) return Math.round(h)+'h';
      const d=h/24;  if(d<365) return Math.round(d)+'d';
      return Math.round(d/365)+'y';
    },
    metricCards(){
      const r=this.last[this.sel]; if(!r) return [];
      // newest stored run (may be failed) — stamp cards when the
      // shown value came from an older ok run
      const newest=this.runs[0];
      const stale=newest && r.id!==newest.id ? this.age(r.started_at) : null;
      const out=[];
      const ms=(id,v,label)=>{out.push({id,band:this.bandName(id,v),
        text:this.fmtv(id,v),label,age:stale})};
      ms('ttfb',r.ttfb_ms,'TTFB'); ms('fcp',r.fcp_ms,'FCP');
      ms('lcp',r.lcp_ms,'LCP');   ms('cls',r.cls,'CLS');
      ms('tbt',r.tbt_ms,'TBT');
      out.push({id:'',band:'na',text:r.request_count??'-',label:'requests',age:stale});
      out.push({id:'',band:'na',text:r.page_bytes?Math.round(r.page_bytes/1024)+'KB':'-',label:'weight',age:stale});
      out.push({id:'',band:'na',text:r.dom_nodes??'-',label:'DOM nodes',age:stale});
      const proto=r.http_protocol||'';
      out.push({id:'proto',band:proto.startsWith('h3')||proto.startsWith('h2')?'good':proto?'ok':'na',
        text:proto||'-',label:'protocol',age:stale});
      // detail_json fields (encoding/tls/dom/cache live per-response)
      const d=this.det||{};
      const smp=(d.samples&&d.samples.length)?d.samples[d.samples.length-1]:{};
      const resp=(smp.responses||d.responses||[]);
      const enc=(resp[0]&&(resp[0].encoding||resp[0].content_encoding))||'';
      out.push({id:'comp',band:(enc==='br'||enc==='zstd')?'good':enc==='gzip'?'ok':enc?'poor':'na',
        text:enc||'-',label:'encoding',age:stale});
      const tls=(d.http&&d.http.tls_version)||'';
      out.push({id:'tls',band:tls.includes('1.3')?'good':tls?'ok':'na',
        text:tls||'-',label:'TLS',age:stale});
      const cv=(d.http&&d.http.cache)||[];
      const tot=cv.length, cacheN=cv.filter(x=>x.cacheable).length;
      const pct=tot?Math.round(cacheN/tot*100):0;
      out.push({id:'cache',band:tot?(pct>=80?'good':pct>=50?'ok':'poor'):'na',
        text:tot?pct+'%':'-',label:'cacheable',age:stale});
      out[7]={id:'',band:'na',text:smp.dom_nodes??r.dom_nodes??'-',label:'DOM nodes',age:stale};
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
      // async refresh: fast poll while a job is queued/running
      const active=this.jobs.some(j=>j.state==='queued'||j.state==='running');
      clearTimeout(this._poll);
      this._poll=setTimeout(()=>{ this.load();
        if(this.sel) this.open(this.sel); }, active?3000:15000);
    },
    back(){
      this.sel=''; this.det=null; this.findings=[]; this.runs=[];
      location.hash='';
    },
    async open(key){
      this.sel=key; location.hash=key;
      this.runs=(await this.j('/api/sites/'+key+'/runs'))||[];
      if(this.runs.length){
        const det=await this.j('/api/runs/'+this.runs[0].id);
        try{ this.det=JSON.parse(det.detail_json);
             const seen=new Set();
             this.findings=(this.det.findings||[]).filter(f=>{
               const k=f.id+'|'+f.title+'|'+(f.detail||'');
               return !seen.has(k)&&(seen.add(k),true)});
           }catch(e){ this.det=null; this.findings=[] }
      }
      this.draw();
    },
    // add/edit/delete monitored sites via the new API
    async addSite(){
      if(!this.addUrl) return;
      const only=this.scanType==='custom'?this.onlySel.join(','):(this.scanType||'');
      await this.j('/api/sites',{method:'POST',body:JSON.stringify({
        url:this.addUrl,schedule:this.addSched,deep:this.scanType==='deep',only})});
      this.addUrl=''; this.addSched=''; await this.load();
    },
    async editSite(s){
      const schedule=prompt('schedule (30m, 6h, daily — empty = manual):', s.schedule||'');
      if(schedule===null) return;
      const only=prompt('only set (empty = all):', s.only||'');
      if(only===null) return;
      const deep=confirm('run deep scans for this site? (attribution + lighthouse)');
      await this.j('/api/sites/'+s.key,{method:'PATCH',
        body:JSON.stringify({schedule,only,deep})});
      await this.load();
    },
    async delSite(s){
      if(!confirm('remove '+s.key+' and its history?')) return;
      await this.j('/api/sites/'+s.key,{method:'DELETE'});
      if(this.sel===s.key){ this.sel=''; location.hash=''; }
      await this.load();
    },
    // scan with the toolbar type picker — site row or detail header
    siteByKey(k){ return this.sites.find(x=>x.key===k)||{key:k} },
    async scanSite(s){
      this.busy=true;
      const only=this.scanType==='custom'?this.onlySel.join(','):(this.scanType==='deep'?'':this.scanType);
      await this.j('/api/scan-now',{method:'POST',body:JSON.stringify({
        key:s.key,deep:this.scanType==='deep',only:only||''})});
      setTimeout(async()=>{ this.busy=false; await this.load();
        if(this.sel===s.key) this.open(s.key); },2000);
    },
    // ad-hoc url scan via POST /api/scan
    async scanUrl(){
      if(!this.addUrl) return;
      this.busy=true;
      const only=this.scanType==='custom'?this.onlySel.join(','):'';
      const r=await this.j('/api/scan',{method:'POST',body:JSON.stringify({
        url:this.addUrl,wait:60,deep:this.scanType==='deep',only})});
      if(!r||r.error){ this.busy=false;
        alert('scan failed: '+((r&&r.error)||'request failed')); return; }
      this.busy=false; this.addUrl=''; await this.load();
    },
    async prune(key){
      const r=await this.j('/api/prune',{method:'POST',body:JSON.stringify({key})});
      alert('pruned '+(r&&r.dropped)+' runs per retention policy');
    },
    async scan(key){
      this.busy=true;
      await this.j('/api/scan-now',{method:'POST',body:JSON.stringify({key})});
      setTimeout(async()=>{ this.busy=false; await this.load(); if(this.sel==key) this.open(key); },2000);
    },
    draw(){
      const rs=[...this.runs].reverse();
      const labels=rs.map(r=>r.started_at.slice(5,16));
      const KB=b=>b?Math.round(b/1024):null;
      const specs=[
        {id:'ttfb',label:'TTFB ms',v:r=>r.ttfb_ms,b:[800,1800]},
        {id:'fcp',label:'FCP ms',v:r=>r.fcp_ms,b:[1800,3000]},
        {id:'lcp',label:'LCP ms',v:r=>r.lcp_ms,b:[2500,4000]},
        {id:'cls',label:'CLS',v:r=>r.cls,b:[0.1,0.25]},
        {id:'tbt',label:'TBT ms',v:r=>r.tbt_ms,b:[200,600]},
        {id:'reqs',label:'requests',v:r=>r.request_count,b:[75,150]},
        {id:'weight',label:'weight KB',v:r=>KB(r.page_bytes),b:[1536,3072]},
      ];
      const bandColor={'good':'#2e7d32','ok':'#f9a825','poor':'#c62828','na':'#97a5a0'};
      for(const sp of specs){
        const el=document.getElementById('tr-'+sp.id); if(!el) continue;
        if(this.charts[sp.id]) this.charts[sp.id].destroy();
        const vals=rs.map(sp.v);
        // point colors by band — green/amber/red like LH score rings
        const ptColor=vals.map(v=>{
          if(v==null||!sp.b) return bandColor.na;
          return bandColor[v>sp.b[1]?'poor':v>sp.b[0]?'ok':'good'];
        });
        const ds=[{label:sp.label,data:vals,
          backgroundColor:'hsl(210 39% 44% / .12)',
          pointBackgroundColor:ptColor,pointBorderColor:ptColor,pointRadius:3,
          // segment colored by destination point's band — the line
          // itself goes green/yellow/red like LH score bands
          segment:{borderColor:c=>{
            const v=vals[c.p1DataIndex];
            if(v==null||!sp.b) return bandColor.na;
            return bandColor[v>sp.b[1]?'poor':v>sp.b[0]?'ok':'good'];
          }},
          tension:.3,fill:true}];
        if(sp.b){ds.push(
          // amber zone: fill between the two cutoff values
          {data:labels.map(()=>sp.b[0]),borderWidth:0,pointRadius:0,fill:false},
          {data:labels.map(()=>sp.b[1]),borderWidth:0,pointRadius:0,
            fill:'-1',backgroundColor:'rgba(249,165,37,.14)'},
          // boundary dashes
          {data:labels.map(()=>sp.b[0]),borderColor:'#2e7d32',borderDash:[5,4],pointRadius:0,borderWidth:1.4},
          {data:labels.map(()=>sp.b[1]),borderColor:'#c62828',borderDash:[5,4],pointRadius:0,borderWidth:1.4});}
        this.charts[sp.id]=new Chart(el,{type:'line',
          data:{labels,datasets:ds},
          options:{plugins:{legend:{display:false}},maintainAspectRatio:false,
            scales:{x:{display:false},y:{ticks:{font:{size:9}}}}}});
      }
    }
  }));
});
