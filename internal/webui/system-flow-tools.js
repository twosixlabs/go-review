(function(root) {
  'use strict';
  function executionState(alternatives) {
    const states=new Set(alternatives.map(a=>a.execution));
    return states.size>1?'mixed':states.values().next().value||'unknown';
  }
  function production(component,report) {
    return component.kind!=='actor'&&((component.members||[]).length>0||
      component.package===report.module||component.package?.startsWith(report.module+'/'));
  }
  const supportingLibraries=new Set(['context','sync','sync/atomic','time','fmt','errors','strings','bytes','strconv',
    'regexp','sort','slices','maps','path','path/filepath','reflect','runtime','encoding/json','encoding/hex','encoding/base64','math','log']);
  function project(report,options={}) {
    const flow=report.systemFlow;
    const empty={nodes:[],edges:[],alternatives:[],groups:[],exchanges:[],limitations:flow?.limitations||[]};
    if(!flow)return {...empty,error:'Architectural source facts are unavailable. Analyze the module to collect them.'};
    if(flow.sourceSHA256!==report.sourceSHA256)return {...empty,error:'Architecture belongs to a different source snapshot.'};
    const components=flow.components||[],byID=new Map(components.map(c=>[c.id,c]));
    const internal=components.filter(c=>production(c,report)),packages=[...new Set(internal.map(c=>c.package))].sort();
    const groups=packages.map(id=>({id,name:id,members:internal.filter(c=>c.package===id).map(c=>c.id)}));
    const alternatives=(flow.alternatives||[]).filter(a=>
      (!options.componentID||a.componentID===options.componentID)&&
      (!options.operationID||(flow.operations||[]).some(op=>op.id===options.operationID&&op.symbolID===a.symbolID)));
    let exchanges=flow.exchanges||[];
    if(options.operationID) {
      const calls=new Set((report.operationSources?.calls||[]).filter(c=>c.callerKey===options.operationID).map(c=>c.callID));
      exchanges=exchanges.filter(e=>(e.callIDs||[]).some(id=>calls.has(id)));
    }else if(options.componentID)exchanges=exchanges.filter(e=>e.from===options.componentID||e.to===options.componentID);
    const nodes=new Map(),membership=new Map(),context=options.level==='context'||!options.level;
    // Context merges dependency invocation forms, preserving the underlying
    // exchanges for drill-down. Package groups do not imply business roles.
    for(const c of components) {
      let id,name,kind,group;
      if(context) {
        if(production(c,report)){id='system';name=report.module;kind='system';}
        else{
          kind=['actor','durable-store'].includes(c.kind)?c.kind:'dependency';
          id='boundary:'+kind;name=kind==='actor'?'External actors':kind==='durable-store'?'External stores':'Dependency contracts';
        }
      }else if(c.id!==options.componentID&&!production(c,report)&&supportingLibraries.has(c.package)) {
        id='supporting-libraries';name='Supporting libraries';kind='support';
      }else if(c.id===options.componentID||options.group&&c.package===options.group||
        c.package&&components.filter(other=>other.package===c.package).length===1) {
        ({id,name,kind}=c);
      }else{
        group=c.package;
        id=group?'package:'+group:'boundary:'+c.kind;
        name=group||'Unresolved boundaries';kind=production(c,report)?'capability':'boundary';
      }
      if(!nodes.has(id))nodes.set(id,{id,name,kind,group,members:[],sources:[],alternatives:[]});
      const node=nodes.get(id);
      node.members.push(c.id);node.sources.push(...c.sources||[]);
      node.alternatives.push(...alternatives.filter(a=>a.componentID===c.id));
      membership.set(c.id,id);
    }
    // A selected component/package shows only its architectural neighbourhood.
    // The complete package catalog remains available independently of this view.
    if(options.level!=='context'&&(options.componentID||options.group)) {
      const focus=new Set(components.filter(c=>c.id===options.componentID||options.group&&c.package===options.group).map(c=>c.id));
      exchanges=exchanges.filter(e=>focus.has(e.from)||focus.has(e.to));
      const visible=new Set([...focus].map(id=>membership.get(id)));
      for(const e of exchanges){visible.add(membership.get(e.from));visible.add(membership.get(e.to));}
      for(const id of nodes.keys())if(!visible.has(id))nodes.delete(id);
    }
    const query=(options.query||'').trim().toLowerCase();
    if(query&&!options.componentID&&!options.group) {
      const matches=new Set([...nodes.values()].filter(n=>
        [n.name,...n.members.map(id=>byID.get(id)?.name||'')].join(' ').toLowerCase().includes(query)).map(n=>n.id));
      for(const id of nodes.keys())if(!matches.has(id)&&id!=='system'&&id!==options.componentID)nodes.delete(id);
    }
    const edges=new Map();
    for(const exchange of exchanges) {
      const from=membership.get(exchange.from),to=membership.get(exchange.to);
      if(from===to||!nodes.has(from)||!nodes.has(to))continue;
      const directions=new Set((exchange.information||[]).map(i=>i.direction));
      if(!directions.size)directions.add('input');
      for(const direction of directions) {
        // Go/defer result signatures are retained as contracts, but their
        // discarded results must not appear as synchronous return delivery.
        if(direction==='output'&&['asynchronous','deferred'].includes(exchange.kind))continue;
        const a=direction==='output'?to:from,b=direction==='output'?from:to;
        const key=JSON.stringify([a,b,direction,context?'contract':exchange.kind]);
        if(!edges.has(key))edges.set(key,{id:key,from:a,to:b,kind:exchange.kind,direction,
          kinds:[],resolution:exchange.resolution,information:[],exchangeIDs:[]});
        const edge=edges.get(key);
        if(!edge.kinds.includes(exchange.kind))edge.kinds.push(exchange.kind);
        if(edge.kinds.length>1)edge.kind='mixed';
        edge.exchangeIDs.push(exchange.id);
        if(exchange.resolution!==edge.resolution)edge.resolution='mixed';
        for(const info of (exchange.information||[]).filter(i=>i.direction===direction))
          if(!edge.information.some(i=>i.type===info.type&&i.name===info.name))edge.information.push(info);
      }
    }
    for(const node of nodes.values()) {
      if(node.members.length===1&&node.id!=='system')node.name=byID.get(node.members[0]).name;
      node.execution=executionState(node.alternatives);
    }
    return {...empty,nodes:[...nodes.values()],edges:[...edges.values()],groups,alternatives,exchanges};
  }
  const api={project,executionState};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;
  else root.GoReviewSystemFlow=api;
})(typeof globalThis==='undefined'?this:globalThis);
