async function j(u,o){const r=await fetch(u,o);return r.json()}
async function load(){
  const sites=await j('/api/sites');
  const tb=document.getElementById('sites');
  tb.innerHTML='';
  for(const s of sites||[]){
    const tr=document.createElement('tr');
    tr.innerHTML=`<td><a href=# onclick="runs('${s.key}');return false">${s.key}</a></td>
      <td>${s.schedule||'manual'}</td><td id=last-${s.id}>-</td>
      <td><button onclick="scan('${s.key}')">scan now</button></td>`;
    tb.appendChild(tr);
  }
  const jobs=await j('/api/jobs');
  document.getElementById('jobs').innerHTML=(jobs||[]).slice(0,5).map(x=>
    `<div class=job>${x.state}: ${x.detail||''}</div>`).join('');
}
async function runs(key){
  const r=await j('/api/sites/'+key+'/runs');
  document.getElementById('runs').innerHTML=(r||[]).slice(0,10).map(x=>
    `<div class=run>#${x.id} ${x.started_at} TTFB ${x.ttfb_ms|0}ms LCP ${x.lcp_ms?x.lcp_ms|0:'—'}ms CLS ${x.cls.toFixed(3)}</div>`).join('');
}
async function scan(key){
  await j('/api/scan-now',{method:'POST',body:JSON.stringify({key})});
  setTimeout(load,1000);
}
load(); setInterval(load,5000);
