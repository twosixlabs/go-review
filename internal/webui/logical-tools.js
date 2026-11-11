(function(root) {
  'use strict';
  const callable=s=>s&&['function','method','closure','test'].includes(s.kind);
  function callableTitle(symbol) {
    const method=/^func \(([^)]+)\)\.([^(\s]+)/.exec(symbol.qualified||'');
    return method?method[1].replace(/^\*/,'').split('.').at(-1)+'.'+method[2]:symbol.name;
  }
  function logicalRelationships(report,id) {
    const symbols=new Map((report.symbols||[]).map(s=>[s.id,s])),symbol=symbols.get(id);
    const local=new Map(),values=new Map(),incoming=new Map(),boundaries=[],conversions=[],unclassifiedReferences=[];
    const group=(map,target,key,evidence)=>{
      if(!map.has(target.id))map.set(target.id,{symbol:target,calls:[],references:[]});
      map.get(target.id)[key].push(evidence);
    };
    for(const call of report.calls||[]) {
      if(call.from===id) {
        const target=symbols.get(call.to);
        if(call.kind==='conversion')conversions.push(call);
        else if(call.kind==='static'&&callable(target))group(local,target,'calls',call);
        else boundaries.push(call);
      }
      if(call.to===id&&call.from!==id&&callable(symbols.get(call.from)))
        group(incoming,symbols.get(call.from),'calls',call);
    }
    for(const ref of report.references||[]) {
      const target=symbols.get(ref.to);
      if(ref.from!==id||!callable(target)||ref.usage==='callee')continue;
      if(ref.usage==='value')group(values,target,'references',ref);
      else unclassifiedReferences.push(ref);
    }
    const order=(a,b)=>{
      const x=(a.calls[0]||a.references[0]).location,y=(b.calls[0]||b.references[0]).location;
      return (x?.offset??0)-(y?.offset??0)||a.symbol.id.localeCompare(b.symbol.id);
    };
    const targets=[...local.values()].sort(order);
    return {symbol,crossPackage:targets.filter(g=>g.symbol.package!==symbol?.package),
      samePackage:targets.filter(g=>g.symbol.package===symbol?.package),references:[...values.values()].sort(order),
      callers:[...incoming.values()],boundaries,conversions,unclassifiedReferences};
  }
  const api={logicalRelationships,callableTitle};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewLogical=api;
})(typeof globalThis==='undefined'?this:globalThis);
