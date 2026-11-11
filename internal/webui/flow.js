(function(root) {
  'use strict';
  function ranks(nodes, edges, entry) {
    const positions = new Map(), adjacent = new Map();
    for (const edge of edges) {
      if (!adjacent.has(edge.from)) adjacent.set(edge.from, []);
      adjacent.get(edge.from).push(edge.to);
    }
    const queue = entry ? [entry] : [];
    if (entry) positions.set(entry, {rank:0});
    for (let i=0; i<queue.length; i++) {
      const current = queue[i];
      for (const next of adjacent.get(current)||[]) {
        if (!positions.has(next)) {
          positions.set(next, {rank:positions.get(current).rank+1});
          queue.push(next);
        }
      }
    }
    const slots = new Map();
    const last = Math.max(0, ...[...positions.values()].map(p=>p.rank))+1;
    for (const node of nodes) {
      if (!positions.has(node.id)) positions.set(node.id,{rank:last});
      const p=positions.get(node.id);
      p.slot=slots.get(p.rank)||0;
      slots.set(p.rank,p.slot+1);
    }
    return positions;
  }
  function compare(a,b) { return a[0]-b[0] || a[1]-b[1]; }
  function coverageFor(location, coverage) {
    if (!location || !coverage) return {state:'unknown',blocks:[]};
    const locations=Array.isArray(location)?location:[location];
    const blocks=(coverage.blocks||[]).filter(b=>locations.some(loc=>b.fileID===loc.fileID &&
      compare([b.startLine,b.startColumn],[loc.endLine,loc.endColumn])<0 &&
      compare([b.endLine,b.endColumn],[loc.line,loc.column])>0));
    if (!blocks.length) return {state:'unknown',blocks};
    const covered=blocks.some(b=>b.count>0), uncovered=blocks.some(b=>b.count===0);
    return {state:covered&&uncovered?'mixed':covered?'covered':'uncovered',blocks};
  }
  // Display-only contraction: original nodes/evidence remain intact. Block
  // identity and edge degrees prevent contracting across control boundaries.
  function compactFlow(flow, expanded=new Set()) {
    const byID=new Map(flow.nodes.map(n=>[n.id,n])),incoming=new Map(),outgoing=new Map();
    for(const edge of flow.edges) {
      if(!incoming.has(edge.to))incoming.set(edge.to,[]);
      if(!outgoing.has(edge.from))outgoing.set(edge.from,[]);
      incoming.get(edge.to).push(edge);outgoing.get(edge.from).push(edge);
    }
    const next=new Map(),previous=new Set();
    const eligible=n=>n?.kind==='statement'&&n.statementBlock&&!n.callIDs?.length;
    for(const n of flow.nodes) {
      const edges=outgoing.get(n.id)||[],edge=edges[0],target=byID.get(edge?.to);
      if(eligible(n)&&eligible(target)&&edges.length===1&&(incoming.get(target.id)||[]).length===1&&
        edge.kind==='sequence'&&!edge.label&&n.statementBlock===target.statementBlock&&n.reachable===target.reachable&&
        n.location?.fileID===target.location?.fileID&&n.location?.endOffset<=target.location?.offset) {
        next.set(n.id,target.id);previous.add(target.id);
      }
    }
    const groups=[],membership=new Map();
    for(const n of flow.nodes) {
      if(previous.has(n.id)||!next.has(n.id))continue;
      const members=[n];
      while(next.has(members.at(-1).id))members.push(byID.get(next.get(members.at(-1).id)));
      const group={
        id:'statements:'+n.id,kind:'statements',label:members.map(m=>m.label).join('\n'),members,
        reachable:n.reachable,location:{...n.location,endLine:members.at(-1).location.endLine,
          endColumn:members.at(-1).location.endColumn,endOffset:members.at(-1).location.endOffset},
        locations:members.map(m=>m.location),callIDs:[],
        comments:[...new Map(members.flatMap(m=>m.comments||[]).map(c=>[JSON.stringify(c),c])).values()]
      };
      groups.push(group);
      if(!expanded.has(group.id))for(const member of members)membership.set(member.id,group);
    }
    const nodes=flow.nodes.flatMap(n=>membership.has(n.id)?membership.get(n.id).members[0].id===n.id?[membership.get(n.id)]:[]:[n]);
    const edges=flow.edges.flatMap(edge=>{
      const from=membership.get(edge.from)?.id||edge.from,to=membership.get(edge.to)?.id||edge.to;
      return membership.has(edge.from)&&from===to?[]:[{...edge,from,to}];
    });
    return {...flow,nodes,edges,groups};
  }
  const api={ranks,coverageFor,compactFlow};
  if(typeof module!=='undefined'&&module.exports) module.exports=api;
  else root.GoReviewFlow=api;
})(typeof globalThis==='undefined'?this:globalThis);
