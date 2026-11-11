'use strict';
const walkthroughTools=GoReviewWalkthrough;

function selectWalkthroughNode(node) {
  mode={...mode,selected:node.id};render();focusGraphNode(node.id);
}
function walkthroughDetail(endpoint,scene,node) {
  if(!node||node.endpoint){endpointRegistrationDetail(endpoint);return;}
  const owner=node.owner||node.symbol||sym(node.ownerID),step=node.step;
  detail(owner?logicalTitle(owner):node.kind,node.expressionCall?node.label:step?.label||node.label);
  const entry=scene.nodes.find(n=>n.instance===node.instance&&n.step?.kind==='entry');
  const origin=entry&&scene.edges.find(e=>e.to===entry.id&&['call','inspection'].includes(e.kind));
  if(origin) {
    const caller=scene.nodes.find(n=>n.id===origin.from);
    button('Return to call site',()=>selectWalkthroughNode(caller),$('detail'));
  }
  if(node.inspection)el('p','This body is shown for inspection, not as the next execution in the caller.',$('detail'),'data-boundary-note');
  if(node.invocation==='deferred'||!node.expressionCall&&step?.kind==='defer')el('p','Deferred execution/recovery not modeled.',$('detail'),'data-boundary-note');
  if(node.invocation==='goroutine'||!node.expressionCall&&step?.kind==='go')el('p','No cross-goroutine ordering modeled.',$('detail'),'data-boundary-note');
  if(node.callIdentityBoundary)el('p','This snapshot has overlapping call-site identities. Reanalyze to recover distinct occurrences; callee bodies can only be inspected here.',$('detail'),'data-boundary-note');
  else if(node.orderBoundary)el('p','Expression ordering/short-circuit paths not expanded. Callee links are inspection-only.',$('detail'),'data-boundary-note');
  const next=scene.edges.filter(e=>e.from===node.id&&e.kind!=='inspection');
  if(next.length) {
    el('h3','What happens next',$('detail'));
    for(const edge of next) {
      const target=scene.nodes.find(n=>n.id===edge.to);
      if(target)button((edge.label||'Next')+' → '+target.label.split('\n')[0].slice(0,70),
        ()=>selectWalkthroughNode(target),$('detail'));
    }
  }
  for(const call of node.calls||[]) {
    const target=sym(call.to),key=node.id+'::'+call.id;
    const local=call.kind==='static'&&target;
    const item=el('section',undefined,$('detail'),'walkthrough-call');
    el('h3',target?logicalTitle(target):call.target,item);
    el('p',(call.invocation||'Invocation unclassified')+' · '+call.kind,item,'muted');
    if(call.reason)el('p',call.reason,item,'muted');
    if(local) {
      const expanded=new Set(mode.expanded||[]),open=expanded.has(key);
      button(open?'Collapse this call':call.invocation==='synchronous'&&!node.orderBoundary&&
        (node.expressionCall||!['defer','go'].includes(step?.kind))?'Follow into '+logicalTitle(target):'Inspect '+logicalTitle(target)+' body',()=>{
        if(open)expanded.delete(key);else expanded.add(key);
        changeEndpoint({expanded:[...expanded],selected:node.id});
        const edge=endpointWalkthroughScene.edges.find(e=>e.from===node.id&&
          e.to.startsWith(key+'::')&&(e.kind==='call'||e.kind==='inspection'));
        if(!open&&edge)selectWalkthroughNode(endpointWalkthroughScene.nodes.find(n=>n.id===edge.to));
        else focusGraphNode(node.id);
      },item);
    }
    button('Inspect call source',()=>source(call.location),item);
  }
  if(step?.members) {
    const members=el('details',undefined,$('detail'));el('summary',step.members.length+' simple statements',members);
    for(const member of step.members)el('pre',member.label,members);
  }
  const metrics=el('div',undefined,$('detail'));
  metricGauge(metrics,'coverage',node.locations||node.location);
  if(node.step?.kind==='entry')metricGauge(metrics,'complexity',null,owner?.cyclomatic);
  for(const comment of node.comments||[])el('p',comment.text,$('detail'),'step-comment');
  if(owner) {
    button('Function control flow',()=>{preferControl=true;go({kind:'symbol',id:owner.id,step:step?.id});},$('detail'));
    button('Value data flow',()=>go({kind:'dataflow',id:owner.id}),$('detail'));
    button('Function relationships',()=>go({kind:'logical',id:owner.id}),$('detail'));
    unitEvidence(owner);
    if(owner.doc) {
      const docs=el('details',undefined,$('detail'));el('summary','Go documentation',docs);el('p',owner.doc,docs);
    }
  }
  if(node.location)button('Inspect step source',()=>source(node.location),$('detail'));
  const limits=el('details',undefined,$('detail'));el('summary','Flow boundaries',limits);
  el('p','Static CFG, not an observed path. Dashed links inspect bodies without adding execution order. Deferred execution, recovery and goroutine scheduling are not modeled.',limits,'muted');
  for(const text of scene.limitations)el('p',text,limits,'muted');
}
let endpointWalkthroughScene;
function renderEndpoint() {
  const registration=selectedEntryPoint();
  const endpoint=registration&&mode.functionSymbolID?{...registration,handlerID:mode.functionSymbolID}:registration;
  $('list').replaceChildren();$('graph').replaceChildren();updateCoverageSummary();
  if(!endpoint){activeScene=null;detail('Endpoint unavailable','Reopen the application overview for the current snapshot.');return;}
  const scene=walkthroughTools.endpointWalkthrough(report,endpoint,mode);
  endpointWalkthroughScene=scene;
  const positions=walkthroughTools.walkthroughPositions(scene);
  const selected=scene.nodes.find(n=>n.id===mode.selected)||
    scene.nodes.find(n=>n.ownerID===endpoint.handlerID&&n.step?.kind==='entry')||scene.nodes[0];
  mode={...mode,selected:selected.id};
  const breadcrumb=$('breadcrumb');breadcrumb.replaceChildren();
  button('Application',()=>go({kind:'application',group:mode.group,query:mode.query}),breadcrumb);
  el('span',' / '+endpointTitle(endpoint),breadcrumb);
  $('legend').textContent='Possible execution flow · true/false branches · loops ↶ · dashed = inspect only';
  el('h3',endpointTitle(endpoint),$('list'));
  button('Choose another endpoint',()=>go({kind:'application',group:mode.group,query:mode.query}),$('list'));
  button('Start at handler',()=>{
    selectWalkthroughNode(scene.nodes.find(n=>n.ownerID===endpoint.handlerID&&n.step?.kind==='entry')||scene.nodes[0]);
  },$('list'));
  button(mode.compact===false?'Group simple steps':'Show individual statements',()=>{
    changeEndpoint({compact:mode.compact===false});
  },$('list'));
  el('h3','Calls & decisions',$('list'));
  const sites=scene.nodes.filter(n=>n.calls?.length||scene.edges.filter(e=>e.from===n.id&&e.kind!=='inspection').length>1)
    .sort((a,b)=>positions.get(a.id).rank-positions.get(b.id).rank||positions.get(a.id).slot-positions.get(b.id).slot);
  for(const node of sites) {
    const item=button(node.label.split('\n')[0].slice(0,90),()=>selectWalkthroughNode(node),$('list'));
    el('span',(sym(node.ownerID)?logicalTitle(sym(node.ownerID))+' · ':'')+'line '+node.location?.line,
      item,'logical-target-package');
  }
  el('p',scene.nodes.length+' displayed steps · branches are alternatives, not a flat list of calls',$('list'),'muted');
  const viewport=mode.viewport;selectedStep=selected.id;
  drawFlow(scene.nodes,scene.edges,scene.entry,node=>{
    mode={...mode,selected:node.id};walkthroughDetail(endpoint,scene,node);
  },positions);
  pan=viewport?{...viewport}:{x:10,y:0,w:650,h:650};view();
  walkthroughDetail(endpoint,scene,selected);
}
