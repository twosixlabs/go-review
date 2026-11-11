(function(root) {
  'use strict';
  function endpointInventory(report,filter={}) {
    const query=(filter.query||'').trim().toLowerCase();
    return (report.httpEndpoints||[]).map(endpoint=>{
      let method='ANY',path='',host='',group='Dynamic patterns';
      if(endpoint.patternKnown) {
        const pattern=endpoint.pattern;
        const split=pattern.search(/[ \t]/);
        const address=split<0?pattern:pattern.slice(split).trimStart();
        if(split>=0)method=pattern.slice(0,split);
        const slash=address.indexOf('/');
        host=slash>0?address.slice(0,slash):'';
        path=slash>=0?address.slice(slash):address;
        const segments=path.split('/').filter(Boolean);
        group='/'+segments.slice(0,segments[0]==='api'?2:1).join('/');
      }
      const title=endpoint.patternKnown?endpoint.pattern||'(empty pattern)':endpoint.patternExpression;
      return {...endpoint,method,path,host,group,title};
    }).filter(row=>(!filter.group||row.group===filter.group)&&
      [row.title,row.handler,row.group,row.reason].join(' ').toLowerCase().includes(query))
      .sort((a,b)=>a.group.localeCompare(b.group)||a.path.localeCompare(b.path)||a.method.localeCompare(b.method)||a.id.localeCompare(b.id));
  }
  function adaptedEntry(entry,report) {
    const symbol=(report.symbols||[]).find(s=>s.id===entry.symbolID);
    const http=entry.kind==='http'?(report.httpEndpoints||[]).find(e=>e.id===entry.id):null;
    if(http)return {...endpointInventory({...report,httpEndpoints:[http]})[0],entryKind:'http',
      resolution:entry.resolution,evidence:entry.evidence};
    return {...entry,handlerID:entry.symbolID,pattern:entry.name,patternKnown:true,
      entryKind:entry.kind,handler:symbol?.qualified||symbol?.name,location:symbol?.location,
      title:entry.name,method:entry.kind==='wasm'?'WIT':entry.kind==='library'?'API':'Go',
      path:entry.name,host:'',group:entry.kind==='wasm'?entry.world||'Wasm exports':
        entry.kind==='library'?symbol?.package||'Public API':entry.kind==='initialization'?'Initialization':'Program entry'};
  }
  function legacyEntries(report) {
    const wasm=(report.files||[]).some(f=>/\/\/(?:go:wasmexport|export)\s/.test(f.source||''));
    const mains=wasm?[]:(report.symbols||[]).filter(s=>s.kind==='function'&&s.name==='main'&&
      (report.packages||[]).some(p=>p.path===s.package&&p.name==='main'))
      .map(s=>adaptedEntry({id:'startup:'+s.id,kind:'main',name:s.qualified||s.name,symbolID:s.id,
        resolution:'resolved',evidence:[]},report));
    return [...endpointInventory(report).map(e=>({...e,entryKind:'http'})),...mains];
  }
  function entryInventory(report,filter={}) {
    const rows=report.entryPoints?(report.entryPoints.entries||[]).map(e=>adaptedEntry(e,report)):legacyEntries(report);
    const query=(filter.query||'').trim().toLowerCase();
    return rows.filter(r=>(!filter.group||r.group===filter.group)&&
      [r.title,r.handler,r.group,r.reason,r.world,r.interface,r.function].join(' ').toLowerCase().includes(query))
      .sort((a,b)=>a.group.localeCompare(b.group)||a.title.localeCompare(b.title)||a.id.localeCompare(b.id));
  }
  function initializationInventory(report) {
    const symbols=report.symbols||[];
    const ids=report.entryPoints?report.entryPoints.initializers||[]:
      symbols.filter(s=>s.kind==='function'&&s.name==='init').map(s=>s.id);
    return ids.map(id=>symbols.find(s=>s.id===id)).filter(Boolean).map(s=>adaptedEntry({
      id:'startup:'+s.id,kind:'initialization',name:s.qualified||s.name,symbolID:s.id,resolution:'resolved',evidence:[]},report));
  }
  function entryByID(report,id) {
    return entryInventory(report).find(e=>e.id===id)||initializationInventory(report).find(e=>e.id===id);
  }
  const api={endpointInventory,entryInventory,initializationInventory,entryByID};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewApplication=api;
})(typeof globalThis==='undefined'?this:globalThis);
