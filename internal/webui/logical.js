'use strict';
const logicalModels=new WeakMap();
const logicalTitle=GoReviewLogical.callableTitle;

function logicalModel(id) {
  if(!logicalModels.has(report))logicalModels.set(report,new Map());
  const models=logicalModels.get(report);
  if(!models.has(id))models.set(id,GoReviewLogical.logicalRelationships(report,id));
  return models.get(id);
}
function logicalSource(location) {
  const file=report.files.find(f=>f.id===location?.fileID);
  return file?file.source.split('\n')[location.line-1]?.trim()||'':'';
}
function openLogical(id,kind) {
  const trail=mode.kind==='logical'?(mode.trail||[{id:mode.id}]):[];
  go({kind:'logical',id,trail:[...trail,{id,via:trail.length?{from:mode.id,kind}:undefined}]});
}
function logicalEvidence(group,kind,parent) {
  const rows=kind==='reference'?group.references:group.calls;
  for(const row of rows) {
    const item=el('details',undefined,parent);
    el('summary','Line '+row.location.line+' · '+(kind==='reference'?'function value':row.kind+' call'),item);
    el('pre',logicalSource(row.location),item);
    if(row.reason)el('p',row.reason,item,'muted');
    button('Inspect source',()=>source(row.location),item);
  }
}
function logicalDetail(model) {
  const s=model.symbol;
  detail(logicalTitle(s),short(s.package));
  metricGauge($('detail'),'coverage',s.location);
  metricGauge($('detail'),'complexity',null,s.cyclomatic);
  if(s.doc)el('p',s.doc,$('detail'));
  const hop=mode.trail?.at(-1)?.via;
  if(hop) {
    const origin=logicalModel(hop.from);
    const group=(hop.kind==='reference'?origin.references:[...origin.crossPackage,...origin.samePackage]).find(g=>g.symbol.id===s.id);
    const evidence=el('details',undefined,$('detail'));
    el('summary',(hop.kind==='reference'?'Referenced by ':'Called by ')+logicalTitle(origin.symbol),evidence);
    el('p',hop.kind==='reference'?'Used as a function value. This may be a callback, assignment or registration; execution is not established.':
      'Static call sites, not a recorded execution path.',evidence,'muted');
    if(group)logicalEvidence(group,hop.kind,evidence);
  }
  button('Inspect statement control flow',()=>{preferControl=true;go({kind:'symbol',id:s.id});},$('detail'));
  button('Inspect value data flow',()=>go({kind:'dataflow',id:s.id}),$('detail'));
  unitEvidence(s);
  if(model.boundaries.length)el('p',model.boundaries.filter(c=>c.kind==='indirect'||c.kind==='unresolved').length+
    ' dynamic/unresolved call sites · '+model.boundaries.length+' total boundary call sites. Expand the boundary card for evidence.',$('detail'),'muted');
  const callers=el('details',undefined,$('detail'));
  el('summary','Called from · '+model.callers.length+' local functions',callers);
  for(const group of model.callers)button(logicalTitle(group.symbol)+' · '+short(group.symbol.package),
    ()=>go({kind:'logical',id:group.symbol.id}),callers);
  const sourceDetails=el('details',undefined,$('detail'));el('summary','Declaration & source',sourceDetails);
  el('pre',s.qualified||s.signature,sourceDetails);
  button('Inspect function source',()=>source(s.location),sourceDetails);
  if(model.conversions.length) {
    const conversions=el('details',undefined,$('detail'));el('summary','Type conversions · '+model.conversions.length,conversions);
    for(const c of model.conversions)button(c.target+' · line '+c.location.line,()=>source(c.location),conversions);
  }
  if(model.unclassifiedReferences.length)el('p',model.unclassifiedReferences.length+
    ' callable references have no syntax classification. Reanalyze to distinguish calls from function values.',$('detail'),'muted');
  const limits=el('details',undefined,$('detail'));el('summary','What this view means',limits);
  el('p','One function and its immediate local implementations. Grouping by package is structural, not a business-role judgment. Calls are static possibilities, not execution order. Dashed references are function values, not proven invocations. Dynamic targets remain unknown.',limits,'muted');
}
function logicalBoundaries(model) {
  detail('Outside-module & dynamic calls',logicalTitle(model.symbol));
  button('Return to function details',()=>logicalDetail(model),$('detail'));
  for(const c of model.boundaries) {
    const row=el('details',undefined,$('detail'));
    el('summary',c.target+' · '+c.kind,row);
    el('p',c.reason||'No verified local callable target.',row,'muted');
    el('pre',logicalSource(c.location),row);
    button('Inspect call source',()=>source(c.location),row);
  }
}
function renderLogical() {
  const model=logicalModel(mode.id),s=model.symbol;
  $('list').replaceChildren();$('graph').replaceChildren();updateCoverageSummary();
  for(const id of ['function-flow','data-flow','call-map','review-map'])$(id).setAttribute('aria-pressed','false');
  $('compact-statements').disabled=true;
  if(!s){activeScene=null;detail('Function unavailable','Select a function in the current snapshot.');return;}
  const trail=mode.trail||[{id:s.id}],breadcrumb=$('breadcrumb');breadcrumb.replaceChildren();
  trail.forEach((hop,i)=>{
    if(i)el('span',hop.via?.kind==='reference'?' ⇢ ':' → ',breadcrumb,'logical-separator');
    const target=sym(hop.id);
    button(target?logicalTitle(target):'Unavailable',()=>go({kind:'logical',id:hop.id,trail:trail.slice(0,i+1)}),breadcrumb);
  });
  $('legend').textContent='STATIC · → calls local implementation · ⇢ function value (not a call) · click to follow';
  const showSame=mode.showSame??model.crossPackage.length===0;
  const cross=model.crossPackage.map(group=>({group,kind:'call',section:'Other packages'}));
  const same=model.samePackage.map(group=>({group,kind:'call',section:'Same package'}));
  const refs=model.references.map(group=>({group,kind:'reference',section:'Function values'}));
  const query=(mode.filter||'').trim().toLowerCase();
  const matches=entry=>(logicalTitle(entry.group.symbol)+' '+entry.group.symbol.package+' '+
    [...entry.group.calls,...entry.group.references].map(row=>logicalSource(row.location)).join('\n')).toLowerCase().includes(query);
  const entries=[...cross,...refs,...(showSame?same:[])].filter(matches);
  const pageSize=8,page=Math.min(mode.page||0,Math.max(0,Math.ceil(entries.length/pageSize)-1));
  const shown=entries.slice(page*pageSize,page*pageSize+pageSize);
  el('h3','Explore from here',$('list'));
  el('p',short(s.package),$('list'),'muted');
  const filter=el('input',undefined,$('list'));filter.type='search';filter.value=mode.filter||'';
  filter.placeholder='Find implementation or source text';filter.setAttribute('aria-label','Filter logical targets');
  filter.oninput=()=>{
    const value=filter.value,start=filter.selectionStart,end=filter.selectionEnd;
    mode={...mode,filter:value,page:0,viewport:undefined};render();
    const next=$('list').querySelector('input');next.focus();next.setSelectionRange(start,end);
  };
  if(model.samePackage.length)button((showSame?'Collapse':'Expand')+' same-package calls · '+model.samePackage.length,()=>{
    mode={...mode,showSame:!showSame,page:0,viewport:undefined};render();
  },$('list'));
  el('p',entries.length+' targets · '+model.boundaries.length+' boundary call sites',$('list'),'muted');
  for(const entry of shown) {
    const target=entry.group.symbol;
    const item=button((entry.kind==='reference'?'⇢ ':'→ ')+logicalTitle(target),()=>openLogical(target.id,entry.kind),$('list'));
    item.title=target.qualified;
    el('span',entry.section+' · '+short(target.package),item,'logical-target-package');
    if(entry.kind==='reference')el('span',logicalSource(entry.group.references[0].location),item,'logical-reference-source');
  }
  if(entries.length)el('p',`${page*pageSize+1}–${Math.min((page+1)*pageSize,entries.length)} / ${entries.length}`,$('list'),'muted');
  for(const [text,next] of [['Previous targets',page-1],['Next targets',page+1]])if(next>=0&&next*pageSize<entries.length)
    button(text,()=>{mode={...mode,page:next,viewport:undefined};render();},$('list'));
  button('Browse this package',()=>go({kind:'package',id:s.package}),$('list'));

  const root={id:s.id,kind:'entry',label:logicalTitle(s)+'\n'+short(s.package)+'\n'+(s.doc?.trim().split('\n')[0]||'Selected implementation'),
    location:s.location,symbol:s,comments:[],reachable:true};
  const nodes=[root],edges=[],positions=new Map([[s.id,{rank:0,slot:0}]]);
  const handlers=new Map();
  for(const [i,entry] of shown.entries()) {
    const target=entry.group.symbol,evidence=entry.kind==='reference'?entry.group.references:entry.group.calls;
    const id=entry.kind+':'+target.id;
    nodes.push({id,kind:entry.kind==='reference'?'function value · '+evidence.length+' site(s)':'local call · '+evidence.length+' site(s)',
      label:logicalTitle(target)+'\n'+short(target.package)+'\n'+(entry.kind==='reference'?logicalSource(evidence[0].location):
        target.doc?.trim().split('\n')[0]||logicalSource(evidence[0].location)),location:target.location,
      symbol:target,comments:[],reachable:true});
    edges.push({from:s.id,to:id,kind:'logical',indirect:entry.kind==='reference',description:entry.kind==='reference'?
      'Function value reference, not a proven invocation':'Verified static local target; not execution order'});
    positions.set(id,{rank:i,slot:1});
    handlers.set(id,()=>openLogical(target.id,entry.kind));
  }
  const addGroup=(id,kind,label,activate)=>{
    nodes.push({id,kind,label,comments:[],reachable:true});
    positions.set(id,{rank:nodes.length-2,slot:1});edges.push({from:s.id,to:id,kind:'summary'});
    handlers.set(id,activate);
  };
  if(!showSame&&model.samePackage.length)addGroup('logical-same','collapsed',model.samePackage.length+' same-package targets\nClick to expand',
    ()=>{mode={...mode,showSame:true,page:0,viewport:undefined};render();});
  if(model.boundaries.length)addGroup('logical-boundary','boundary',model.boundaries.length+' outside-module / dynamic sites\nClick to inspect unresolved and external calls',
    ()=>logicalBoundaries(model));
  if(!entries.length&&!query)el('p','No local call targets or function-value references found. This is not proof of no runtime behavior.',$('list'),'muted');
  else if(!entries.length)el('p','No visible targets match. Same-package calls may be collapsed.',$('list'),'muted');
  const viewport=mode.viewport;selectedStep=s.id;
  drawFlow(nodes,edges,s.id,node=>{if(handlers.has(node.id))handlers.get(node.id)();else logicalDetail(model);},positions);
  pan=viewport?{...viewport}:{x:10,y:0,w:800,h:650};view();
  logicalDetail(model);
}

const beforeLogicalRender=render,beforeLogicalGo=go,beforeLogicalView=view,beforeLogicalKey=graphKey;
go=function(next,remember=true){
  if(next.kind==='symbol'&&['packages','package'].includes(mode.kind)&&
    ['function','method','closure','test'].includes(sym(next.id)?.kind))next={...next,kind:'logical'};
  beforeLogicalGo(next,remember);
};
graphKey=function(){
  return mode.kind==='logical'?'logical:'+mode.id+':'+mode.showSame+':'+(mode.filter||'')+':'+(mode.page||0):beforeLogicalKey();
};
view=function(){beforeLogicalView();if(mode.kind==='logical')mode={...mode,viewport:{...pan}};};
render=function(){
  if(!report)return;
  const logical=mode.kind==='logical';$('logical-flow').setAttribute('aria-pressed',String(logical));
  if(logical) {
    document.querySelector('.graph-hint').textContent='Drag nodes to rearrange · drag background to pan · Alt + arrows moves a focused node';
    renderLogical();
  } else beforeLogicalRender();
};
$('logical-flow').onclick=()=>{
  if(['logical','symbol','dataflow'].includes(mode.kind))go({kind:'logical',id:mode.id});
  else if(mode.kind==='review'&&mode.id)go({kind:'logical',id:mode.id});
  else detail('Choose a function to explore','Select a function from a package or search result. Logical flow follows local implementations and function values without expanding framework internals.');
};
for(const id of ['function-flow','data-flow','call-map']) {
  const previous=$(id).onclick;
  $(id).onclick=()=>{
    if(mode.kind!=='logical'){previous();return;}
    if(id==='data-flow')go({kind:'dataflow',id:mode.id});
    else {preferControl=id==='function-flow';go({kind:'symbol',id:mode.id});}
  };
}
const beforeLogicalSearch=$('search').oninput;
$('search').oninput=()=>{
  if(mode.kind==='logical'&&$('search').value.trim())go({kind:'packages'});
  else beforeLogicalSearch();
};
const beforeLogicalReset=$('reset-layout').onclick;
$('reset-layout').onclick=()=>{
  if(mode.kind==='logical'){mode={...mode,viewport:undefined};}
  beforeLogicalReset();
};
