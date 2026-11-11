(function(root) {
  'use strict';
  const common=typeof module!=='undefined'&&module.exports;
  const graph=common?require('./graph-tools.js'):root.GoReviewGraph;
  const assessment=common?require('./assessment-tools.js'):root.GoReviewAssessment;

  // Occurrences retain call-site identity. A group's measurement scope, unlike
  // its topology, is a set of source definitions, never a sum of occurrences.
  function hierarchy(report,scene) {
    if(scene.error)return {error:scene.error};
    const symbols=new Map((report.symbols||[]).map(s=>[s.id,s]));
    const byID=new Map(scene.nodes.map(n=>{
      const symbol=symbols.get(n.symbolID);
      return [n.id,{...n,title:n.phase==='entry'||n.phase==='completion'?n.label.split('\n')[0]:
        symbol?.name||n.label.split('\n').slice(0,2).join(' · '),
        description:symbol?.doc||'',children:[],parentID:null}];
    }));
    for(const n of byID.values()) {
      if(n.phase!=='async-work'&&n.activityID&&n.activityID!==scene.entry) {
        if(!byID.has(n.activityID))return {error:'Missing source owner for '+n.id+': '+n.activityID};
        n.parentID=n.activityID;
      }
      const seen=new Set([n.id]);
      let owner=n;
      while(owner.parentID||(owner.phase!=='async-work'&&owner.activityID&&owner.activityID!==scene.entry)) {
        const id=owner.parentID||owner.activityID;
        if(seen.has(id))return {error:'Source ownership cycle at '+id};
        seen.add(id);owner=byID.get(id);
        if(!owner)return {error:'Missing source owner '+id};
      }
    }
    for(const n of byID.values())if(n.parentID)byID.get(n.parentID).children.push(n.id);
    function members(n) {
      const descendants=n.children.flatMap(id=>members(byID.get(id)));
      n.sourceIDs=[n.id,...descendants];
      n.symbolIDs=[...new Set(n.sourceIDs.map(id=>byID.get(id).symbolID).filter(Boolean))];
      n.alternatives=[...new Map(n.sourceIDs.flatMap(id=>byID.get(id).alternatives||[]).map(a=>[a.id,a])).values()];
      return n.sourceIDs;
    }
    const roots=[...byID.values()].filter(n=>!n.parentID).map(n=>n.id);
    roots.forEach(id=>members(byID.get(id)));
    return {...scene,byID,roots};
  }

  // Contract only hidden children into the nearest visible owner. Guard unions
  // are source alternatives, not conjunctions or observed execution sequences.
  function project(tree,expandedIDs) {
    if(tree.error)return tree;
    const expanded=new Set(expandedIDs),nodes=[],visible=new Set();
    function visit(id) {
      const node=tree.byID.get(id);
      nodes.push({...node,expanded:expanded.has(id)&&node.children.length>0});
      visible.add(id);
      if(expanded.has(id))node.children.forEach(visit);
    }
    tree.roots.forEach(visit);
    function owner(id) {
      while(!visible.has(id))id=tree.byID.get(id).parentID;
      return id;
    }
    const edges=new Map();
    for(const edge of tree.edges) {
      const from=owner(edge.from),to=owner(edge.to);
      if(from===to&&edge.from!==edge.to)continue;
      const kind=edge.kind||'control',key=JSON.stringify([from,to,kind]);
      if(!edges.has(key))edges.set(key,{from,to,kind,segments:[],guards:[],alternatives:[]});
      edges.get(key).segments.push(edge);
    }
    for(const edge of edges.values()) {
      edge.guards=[...new Set(edge.segments.flatMap(e=>e.guards||[]))];
      edge.alternatives=[...new Map(edge.segments.flatMap(e=>e.alternatives||[]).map(a=>[a.id,a])).values()];
    }
    return {...tree,nodes,edges:[...edges.values()],sourceIDs:[...tree.byID.keys()]};
  }

  function metrics(report,ids) {
    const symbolIDs=[...new Set(ids)],symbols=new Map((report.symbols||[]).map(s=>[s.id,s]));
    const members=symbolIDs.map(id=>symbols.get(id));
    const locations=members.filter(Boolean).map(s=>s.location).filter(Boolean);
    const coverage=graph.coverageMetric(locations,report.coverage);
    const values=members.map(s=>s?.cyclomatic).filter(v=>Number.isFinite(v)&&v>=1);
    const complexity={...graph.complexityMetric(values.length?Math.max(...values):undefined),
      aggregation:'maximum',unknown:members.length-values.length};
    complexity.maximumSymbolIDs=members.filter(s=>s&&s.cyclomatic===complexity.value).map(s=>s.id);
    const levels=symbolIDs.map(symbolID=>assessment.indicators(report,{symbolID}).level);
    const level=levels.reduce((worst,l)=>assessment.rank[l]>assessment.rank[worst]?l:worst,'not-assessed');
    return {memberCount:symbolIDs.length,scope:'represented-unique-operation-definitions',symbolIDs,
      coverage:{...coverage,aggregation:'statement-weighted-unique-blocks'},complexity,
      architecture:{level,aggregation:'highest-supported-rule-concern',
        unknown:levels.filter(l=>l==='not-assessed').length}};
  }
  // A longest forward continuation gives early exits a side lane without
  // guessing success from names, coverage or branch polarity. DFS back edges
  // remain in the scene but cannot make the layout spine cycle.
  function layout(scene,ids) {
    const byID=new Map(scene.nodes.map(n=>[n.id,n])),scope=new Set(ids);
    const background=ids.filter(id=>byID.get(id).phase==='async-work');
    const foreground=ids.filter(id=>!background.includes(id)),eligible=new Set(foreground);
    const adjacency=new Map(foreground.map(id=>[id,[]]));
    function owner(id) {
      const seen=new Set();
      while(id&&!scope.has(id)&&!seen.has(id)) {
        seen.add(id);id=byID.get(id)?.parentID;
      }
      return scope.has(id)?id:null;
    }
    for(const edge of scene.edges) {
      if(edge.kind==='asynchronous')continue;
      const from=owner(edge.from),to=owner(edge.to);
      if(from!==to&&eligible.has(from)&&eligible.has(to)&&!adjacency.get(from).includes(to))
        adjacency.get(from).push(to);
    }
    const visiting=new Set(),paths=new Map();
    let ambiguous=false;
    function longest(id) {
      if(paths.has(id))return paths.get(id);
      visiting.add(id);
      let best=[];
      for(const to of adjacency.get(id)) {
        if(visiting.has(to))continue;
        const path=longest(to);
        if(path.length>best.length)best=path;
        else if(path.length===best.length)ambiguous=true;
      }
      visiting.delete(id);
      const path=[id,...best];paths.set(id,path);return path;
    }
    const entry=eligible.has(scene.entry)?scene.entry:foreground[0];
    const spine=entry?longest(entry):[],placed=new Set(spine);
    const placements=spine.map((id,i)=>({id,column:1,row:i+1}));
    const occupied=new Set();
    function branch(id,row) {
      if(placed.has(id))return;
      placed.add(id);
      while(occupied.has(row))row++;
      occupied.add(row);placements.push({id,column:2,row});
      for(const to of adjacency.get(id))branch(to,row+1);
    }
    spine.forEach((id,i)=>adjacency.get(id).forEach(to=>branch(to,i+1)));
    foreground.forEach(id=>branch(id,1));
    return {spine,placements,background,ambiguous};
  }
  const api={hierarchy,project,metrics,layout};
  if(common)module.exports=api;else root.GoReviewVisual=api;
})(typeof globalThis==='undefined'?this:globalThis);
