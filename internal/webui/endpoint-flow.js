'use strict';
const beforeOverallEndpoint=renderEndpoint;
const overallFlowCache=new WeakMap();
let overallEndpointScene;

function overallStageDetail(endpoint,scene,node) {
  detail(node.title||node.label.replace(/\n/g,' · '),node.description||'Overall control and information flow. Supporting statements remain collapsed.');
  button('Entry declaration & evidence',()=>endpointRegistrationDetail(endpoint),$('detail'));
  if(node.boundary)el('p',node.boundary,$('detail'),'error');
  if(node.resolution==='inferred')el('p','Target inferred from production wiring, not runtime dispatch proof.',$('detail'),'muted');
  if(node.resolution==='unresolved')el('p','Unresolved boundary; no implementation or return behavior is asserted.',$('detail'),'muted');
  if(node.invocation==='goroutine')el('p','Launched work has no inferred completion ordering with its caller continuation.',$('detail'),'muted');
  if(node.invocation==='deferred')el('p','This schedules deferred work; its execution ordering is not expanded.',$('detail'),'muted');
  const symbol=sym(node.symbolID);
  if(symbol) {
    if(node.collapsed||node.phase==='async-work'||node.operations?.length)
      button('Review activity',()=>scene.byID?visualFocus(node,scene):
        go({...mode,flowSymbolID:symbol.id,detailLevel:undefined,
          expandedActivities:[],selected:undefined,viewport:undefined,reviewScroll:undefined}),$('detail'));
    if(mode.detailLevel!=='operations')
      button('Inspect operation map',()=>go({...mode,detailLevel:'operations',selected:node.id,viewport:undefined}),$('detail'));
    button('Inspect function steps',()=>go({...mode,detailLevel:'function',functionSymbolID:symbol.id,
      selected:undefined,viewport:undefined}),$('detail'));
    if(symbol.doc&&symbol.doc!==node.description)el('p',symbol.doc,$('detail'));
    for(const comment of symbol.comments||[])el('p',comment.text,$('detail'));
  }
  if(node.information?.length) {
    el('h3','Information exchanged',$('detail'));
    for(const i of node.information)el('p',(i.direction==='output'?'Output':'Input')+': '+i.type+(i.name?' · '+i.name:''),$('detail'));
    el('p','Source contracts, not recorded variable values. Async/deferred result signatures do not establish return delivery.',$('detail'),'muted');
  }
  const next=scene.edges.filter(e=>e.from===node.id);
  if(next.length) {
    el('h3','Possible continuations',$('detail'));
    for(const e of next) {
      const target=scene.nodes.find(n=>n.id===e.to);
      button((e.kind==='asynchronous'?'Async work':e.label||'Possible continuation')+' → '+(target.title||target.label.replace(/\n/g,' · ')),
        ()=>selectOverallActivity(endpoint,scene,target),$('detail'));
      for(const guard of e.guards||[])el('p',guard,$('detail'),'muted');
    }
  }
    if(node.operations?.length) {
      el('h3','Responsibilities & boundaries',$('detail'));
      el('p','Nested source operations within this activity, not a chronological execution record. Expand the activity to review its control flow.',$('detail'),'muted');
      for(const operation of node.operations) {
        const item=el('details',undefined,$('detail'),'activity-operation');
        el('summary',operation.label.replace(/\n/g,' · '),item);
        if(operation.resolution==='unresolved')el('p','Unresolved target: inspect the source before assuming its behavior.',item,'error');
        if(operation.boundary)el('p',operation.boundary,item,'error');
        if(operation.invocation!=='synchronous')el('p','Invocation: '+operation.invocation,item,'muted');
        for(const i of operation.information||[])el('p',i.direction+': '+i.type,item);
        systemFlowLocation(operation.location,item);
      }
    }
  const alternatives=el('details',undefined,$('detail'));
  el('summary','Relevant branch evidence · '+node.alternatives.length,alternatives);
  systemFlowLazy(alternatives,()=>{for(const a of node.alternatives)systemFlowAlternative(a,alternatives);});
  const counts=new Map();
  for(const a of node.alternatives)counts.set(a.execution,(counts.get(a.execution)||0)+1);
  el('p',[...counts].map(([state,count])=>count+' '+state).join(' · ')||'No branch evidence for this stage.',$('detail'),'muted');
  systemFlowLocation(node.location,$('detail'));
  if(node.phase==='entry'&&!mode.flowSymbolID&&endpoint.entryKind!=='startup')
    systemFlowLocation(endpoint.location,$('detail'),'Inspect registration source');
  systemFlowProvenance($('detail'),node.componentID);
  const limits=el('details',undefined,$('detail'));el('summary','Projection & limits',limits);
  for(const text of scene.limitations)el('p',text,limits,'muted');
}

function selectOverallActivity(endpoint,scene,node) {
  mode={...mode,selected:node.id};
  if(mode.detailLevel==='operations'){render();focusGraphNode(node.id);return;}
  visualSelect(endpoint,scene,node);
}

renderEndpoint=function() {
  $('activity-overview').replaceChildren();
  if(mode.detailLevel==='function'){beforeOverallEndpoint();return;}
  const endpoint=selectedEntryPoint();
  $('list').replaceChildren();$('graph').replaceChildren();activeScene=null;updateCoverageSummary();
  if(!endpoint){detail('Entry point unavailable','Return to Application and choose an entry point in this snapshot.');return;}
  if(!overallFlowCache.has(report))overallFlowCache.set(report,new Map());
  const cached=overallFlowCache.get(report),key=endpoint.id+':'+(mode.flowSymbolID||'');
  if(!cached.has(key))cached.set(key,GoReviewQuery.rawScene(report,endpoint,mode.flowSymbolID));
  const scene=cached.get(key);
  overallEndpointScene=scene;
  const breadcrumb=$('breadcrumb');breadcrumb.replaceChildren();
  button('Application',()=>go({kind:'application',group:mode.group,query:mode.query}),breadcrumb);
  el('span',' / '+endpointTitle(endpoint)+' / Overall flow'+
    (mode.flowSymbolID?' / '+(sym(mode.flowSymbolID)?.name||'operation'):''),breadcrumb);
  $('legend').textContent='Overall control → possible continuation · dashed → async work · expand source-owned groups for detail';
  el('h3','Overall flow',$('list'));
  button('Choose another entry point',()=>go({kind:'application',group:mode.group,query:mode.query}),$('list'));
  button('Entry declaration & evidence',()=>endpointRegistrationDetail(endpoint),$('list'));
  button((mode.reviewSetup?'Hide':'Show')+' review setup & tools',()=>{
    mode={...mode,reviewSetup:!mode.reviewSetup};render();
  },$('list'));
  if(mode.flowSymbolID)button('Entry-point flow',()=>go({...mode,flowSymbolID:undefined,selected:undefined,viewport:undefined}),$('list'));
  if(scene.error){detail('Overall flow unavailable',scene.error);return;}
  if(mode.detailLevel!=='operations') {
    renderVisualReview(endpoint,scene);
    return;
  }
  button('Activity overview',()=>go({...mode,detailLevel:undefined,selected:undefined}),$('list'));
  for(const node of scene.nodes)
    button(node.label.replace(/\n/g,' · '),()=>{
      mode={...mode,selected:node.id};render();focusGraphNode(node.id);
    },$('list'));
  el('p','Helpers and local statements are collapsed. Select a stage for contracts and branches; function specifics are optional.',$('list'),'muted');
  const selected=scene.nodes.find(n=>n.id===mode.selected)||scene.nodes[0];
  mode={...mode,selected:selected.id};selectedStep=selected.id;
  const viewport=mode.viewport;
  drawFlow(scene.nodes,scene.edges,scene.entry,node=>{
    mode={...mode,selected:node.id};overallStageDetail(endpoint,scene,node);
  },walkthroughTools.walkthroughPositions(scene));
  if(viewport){pan={...viewport};view();}
  overallStageDetail(endpoint,scene,selected);
};
const beforeOverallKey=graphKey;
graphKey=function() {
  return mode.kind==='endpoint'&&mode.detailLevel!=='function'?
    'endpoint-overall:'+mode.id+':'+(mode.flowSymbolID||''):beforeOverallKey();
};
const beforeActivityRender=render;
render=function() {
  const activity=mode.kind==='endpoint'&&!mode.detailLevel;
  document.body.classList.toggle('activity-review',activity);
  document.body.classList.toggle('activity-setup',activity&&!!mode.reviewSetup);
  $('activity-overview').hidden=!activity;
  beforeActivityRender();
  if(activity) {
    $('graph').style.display='none';
    $('canvas').querySelector('.tools').hidden=true;
    document.querySelector('.graph-hint').hidden=true;
  }
};
