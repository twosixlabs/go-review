(function(root) {
  'use strict';
  const flow=typeof module!=='undefined'&&module.exports?require('./flow.js'):root.GoReviewFlow;
  const logical=typeof module!=='undefined'&&module.exports?require('./logical-tools.js'):root.GoReviewLogical;
  function endpointWalkthrough(report,endpoint,state={}) {
    const symbols=new Map((report.symbols||[]).map(s=>[s.id,s]));
    const flows=new Map((report.controlFlows||[]).map(f=>[f.symbolID,f]));
    const calls=new Map((report.calls||[]).map(c=>[c.id,c]));
    const expanded=new Set(state.expanded||[]),nodes=[],edges=[],limitations=new Set();
    const rootID=endpoint.id;
    nodes.push({id:rootID,kind:['startup','main','initialization'].includes(endpoint.entryKind)?'Startup entry':
      endpoint.entryKind==='wasm'?'Wasm export':endpoint.entryKind==='library'?'Public API':'HTTP endpoint',label:endpoint.patternKnown?endpoint.pattern:endpoint.patternExpression,
      endpoint,comments:[],calls:[],reachable:true});
    function instantiate(symbolID,prefix,depth) {
      const owner=symbols.get(symbolID),original=flows.get(symbolID);
      if(!owner)return null;
      if(!original?.nodes.length||depth>8||nodes.length>1200) {
        const reason=!original?.nodes.length?'Control flow unavailable':'Expansion limit reached; open this function separately';
        const id=prefix+'::unavailable';
        nodes.push({id,ownerID:symbolID,symbol:owner,kind:'boundary',label:logical.callableTitle(owner)+'\n'+reason,
          comments:[],calls:[],reachable:true,location:owner.location});
        limitations.add(reason);
        return {entry:id,exit:null};
      }
      for(const text of original.limitations||[])limitations.add(text);
      const compact=state.compact!==false?flow.compactFlow(original):original;
      const visible=compact.nodes.filter(n=>n.reachable!==false&&
        (n.kind!=='exit'||compact.edges.some(e=>e.to===n.id&&e.kind!=='panic')));
      const ids=new Map(visible.map(n=>[n.id,prefix+'::'+n.id]));
      const links=compact.edges.filter(e=>ids.has(e.from)&&ids.has(e.to));
      const starts=new Map(ids),localNodes=[],localEdges=[];
      for(const step of visible) {
        const sourceIDs=step.callIDs||[],callIdentityBoundary=new Set(sourceIDs).size!==sourceIDs.length;
        if(callIdentityBoundary)limitations.add('Call-site identities overlap in this snapshot; reanalyze to recover distinct occurrences.');
        const id=ids.get(step.id),sites=[...new Set(sourceIDs)].map(id=>calls.get(id)).filter(Boolean)
          .sort((a,b)=>(a.evaluationOrder||a.location?.offset||0)-(b.evaluationOrder||b.location?.offset||0));
        const name=logical.callableTitle(owner);
        const ordered=step.callOrderKnown&&sites.every(c=>c.evaluationOrder>0);
        const orderBoundary=sites.length>0&&(callIdentityBoundary||step.callOrderKnown===false||sites.length>1&&!ordered||
          step.callOrderKnown===undefined&&/&&|\|\|/.test(step.label));
        const split=sites.length>1&&ordered&&!orderBoundary;
        const timing=['defer','go'].includes(step.kind)?step.kind:null;
        const kind=step.kind==='entry'?'enter '+name:step.kind==='exit'?'return from '+name:
          timing||(links.filter(e=>e.from===step.id).length>1?'decision':step.kind);
        const target=sites.length===1?symbols.get(sites[0].to):null;
        const label=step.kind==='entry'||step.kind==='exit'?name:
          (target?logical.callableTitle(target)+'\n':'')+step.label;
        const node={...step,id,step,ownerID:symbolID,owner,instance:prefix,kind,label,calls:split?[]:sites,orderBoundary,callIdentityBoundary,
          symbol:step.kind==='entry'?owner:undefined};
        if(split) {
          node.label='Complete step\n'+step.label;
          for(const [i,call] of sites.entries()) {
            const callID=id+'::evaluate:'+call.id,target=symbols.get(call.to);
            const invocation=call.invocation;
            const expression=(report.files||[]).find(f=>f.id===call.location?.fileID)?.source;
            const text=expression===undefined?call.target||'':
              new TextDecoder().decode(new TextEncoder().encode(expression).subarray(call.location.offset,call.location.endOffset));
            const kind=invocation==='deferred'?'defer':invocation==='goroutine'?'go':'call';
            const child={...node,id:callID,kind,calls:[call],orderBoundary:false,location:call.location,
              label:(target?logical.callableTitle(target):call.target)+'\n'+text,
              invocation,expressionCall:true};
            localNodes.push(child);
            if(i===0)starts.set(step.id,callID);
            localEdges.push({from:callID,to:i+1<sites.length?id+'::evaluate:'+sites[i+1].id:id,kind:'sequence'});
          }
        }
        localNodes.push(node);
      }
      for(const edge of links) {
        if(edge.kind==='panic')continue;
        localEdges.push({...edge,from:ids.get(edge.from),to:starts.get(edge.to)});
      }
      nodes.push(...localNodes);
      const replacements=new Set();
      for(const node of localNodes)for(const call of node.calls) {
        const expansionID=node.id+'::'+call.id;
        if(!expanded.has(expansionID)||call.kind!=='static'||!symbols.has(call.to))continue;
        const childStart=nodes.length,child=instantiate(call.to,expansionID,depth+1);
        if(!child)continue;
        // Multi-call/short-circuit expressions and scheduled work are
        // inspection branches, never invented execution or synchronization.
        const synchronous=node.calls.length===1&&!node.orderBoundary&&call.invocation==='synchronous'&&
          (node.expressionCall||!['go','defer'].includes(node.step.kind))&&child.executable;
        if(synchronous) {
          replacements.add(node.id);
          edges.push({from:node.id,to:child.entry,kind:'call',label:'call',description:'Enter this call occurrence'});
          for(const edge of child.exit?localEdges.filter(e=>e.from===node.id):[])edges.push({...edge,from:child.exit,
            kind:'return-to-caller',description:'On normal return, continue at the caller'});
        } else {
          for(const inspected of nodes.slice(childStart)) {
            inspected.inspection=true;
            if(inspected.step?.kind==='entry')inspected.kind='inspect body · '+inspected.kind;
          }
          edges.push({from:node.id,to:child.entry,kind:'inspection',label:call.invocation==='deferred'?'deferred body':
            call.invocation==='goroutine'?'goroutine body':'inspect callee',indirect:true,
            description:'Implementation detail only; does not establish expression order, deferred execution or goroutine synchronization'});
        }
      }
      edges.push(...localEdges.filter(e=>!replacements.has(e.from)));
      const exit=visible.find(n=>n.kind==='exit');
      return {entry:starts.get(original.entry),exit:exit?ids.get(exit.id):null,executable:true};
    }
    const handler=instantiate(endpoint.handlerID,rootID+'::handler',0);
    if(handler?.entry)edges.push({from:rootID,to:handler.entry,kind:'registration',label:'handler'});
    const successors=new Map(),reachable=new Set([rootID]),queue=[rootID];
    for(const edge of edges) {
      if(!successors.has(edge.from))successors.set(edge.from,[]);
      successors.get(edge.from).push(edge.to);
    }
    for(let i=0;i<queue.length;i++)for(const target of successors.get(queue[i])||[]) {
      if(reachable.has(target))continue;
      reachable.add(target);queue.push(target);
    }
    return {entry:rootID,nodes:nodes.filter(n=>reachable.has(n.id)),
      edges:edges.filter(e=>reachable.has(e.from)&&reachable.has(e.to)),limitations:[...limitations]};
  }
  function walkthroughPositions(scene) {
    const ids=new Set(scene.nodes.map(n=>n.id)),out=new Map(),back=new Set(),colors=new Map();
    for(const edge of scene.edges) {
      if(!ids.has(edge.from)||!ids.has(edge.to))continue;
      if(!out.has(edge.from))out.set(edge.from,[]);
      out.get(edge.from).push(edge);
    }
    function visit(id) {
      colors.set(id,1);
      for(const edge of out.get(id)||[]) {
        if(edge.kind==='loop'||colors.get(edge.to)===1){back.add(edge);continue;}
        if(!colors.has(edge.to))visit(edge.to);
      }
      colors.set(id,2);
    }
    for(const node of scene.nodes)if(!colors.has(node.id))visit(node.id);
    const incoming=new Map(scene.nodes.map(n=>[n.id,0])),ranks=new Map(scene.nodes.map(n=>[n.id,0]));
    for(const edge of scene.edges)if(!back.has(edge)&&ids.has(edge.to)&&ids.has(edge.from))
      incoming.set(edge.to,incoming.get(edge.to)+1);
    const queue=scene.nodes.filter(n=>!incoming.get(n.id)).map(n=>n.id);
    for(let i=0;i<queue.length;i++)for(const edge of out.get(queue[i])||[]) {
      if(back.has(edge))continue;
      ranks.set(edge.to,Math.max(ranks.get(edge.to),ranks.get(edge.from)+1));
      incoming.set(edge.to,incoming.get(edge.to)-1);
      if(!incoming.get(edge.to))queue.push(edge.to);
    }
    const slots=new Map(),positions=new Map();
    for(const node of scene.nodes) {
      const rank=ranks.get(node.id),slot=slots.get(rank)||0;
      positions.set(node.id,{rank,slot});slots.set(rank,slot+1);
    }
    return positions;
  }
  const api={endpointWalkthrough,walkthroughPositions};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewWalkthrough=api;
})(typeof globalThis==='undefined'?this:globalThis);
