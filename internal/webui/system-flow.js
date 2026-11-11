'use strict';
const systemFlowTools=GoReviewSystemFlow;
function systemFlowLocation(location,parent,title='Inspect source') {
  if(!location?.fileID)return;
  const file=report.files.find(f=>f.id===location.fileID);
  button(title+(file?' · '+file.path+':'+location.line:''),()=>source(location),parent);
}
function systemFlowLazy(section,build) {
  let built=false;
  section.addEventListener('toggle',()=>{
    if(section.open&&!built){built=true;build();}
  });
}
function systemFlowProvenance(parent,componentID=mode.componentID) {
  const evidence=el('details',undefined,parent);
  el('summary','Test selection & source identity',evidence);
  el('p','Snapshot '+report.sourceSHA256,evidence,'muted');
  if(!report.unitEvidence)el('p',report.coverage?'Imported statement coverage; no retained selected-unit run.':'No selected unit evidence. Alternatives remain unknown.',evidence);
  for(const run of report.unitEvidence?.runs||[]) {
    el('p',run.selection.package+' :: '+(report.unitEvidence.allTests?'all in-scope tests':run.selection.tests.join(', '))+' · '+run.status,evidence);
    el('p','Actually matched: '+((run.matchedTests||[]).join(', ')||'none'),evidence,'muted');
    if(run.error)el('pre',run.error,evidence,'error');
    if(run.coveragePath)el('p','Coverage artifact: '+run.coveragePath,evidence,'muted');
    if(run.runtimeTracePath)el('p','Standard Go trace artifact: '+run.runtimeTracePath,evidence,'muted');
    if(run.runtime)systemFlowRuntime(run.runtime,evidence,componentID);
  }
  el('p','Coverage is aggregate for each exact selection, not attribution to an individual test. Covered alternatives are not a chronological sequence.',evidence,'muted');
}
function systemFlowRuntime(runtime,parent,componentID=mode.componentID) {
  const section=el('details',undefined,parent);
  el('summary','Standard runtime observations · '+runtime.status,section);
  if(runtime.error)el('pre',runtime.error,section,'error');
  if(runtime.toolVersion)el('p',runtime.toolVersion+' · '+runtime.format,section,'muted');
  for(const observation of runtime.observations||[]) {
    if(componentID&&!(observation.componentIDs||[]).includes(componentID))continue;
    const row=el('details',undefined,section);
    el('summary','Observed '+observation.kind+' · G'+observation.relatedGoroutine+' → G'+observation.goroutine+
      (observation.reason?' · '+observation.reason:''),row);
    el('p','Trace time '+observation.timeNS+' ns. Event-associated stacks, not a call-by-call history.',row,'muted');
    for(const frame of [...observation.stack||[],...observation.targetStack||[]])
      systemFlowLocation(frame.location,row,frame.function);
    if(observation.exchangeIDs?.length)
      el('p',observation.exchangeIDs.length+' source-line asynchronous exchange candidate(s).',row,'muted');
  }
  for(const text of runtime.limitations||[])el('p',text,section,'muted');
}
function systemFlowAlternative(alternative,parent) {
  const row=el('details',undefined,parent,'system-alternative state-'+alternative.execution);
  el('summary',alternative.decision+' · '+alternative.label+' · '+alternative.execution,row);
  el('p',alternative.reason||'No matching execution witness.',row);
  if(alternative.requires?.length) {
    const requirements=alternative.requires.map(id=>{
      const a=report.systemFlow.alternatives.find(a=>a.id===id);
      return a?a.decision+' · '+a.label:id;
    });
    el('p','Requires enclosing alternative: '+requirements.join('; '),row,'muted');
  }
  systemFlowLocation(alternative.location,row);
  for(const witness of alternative.witnesses||[]) {
    el('p',`${witness.startLine}:${witness.startColumn}–${witness.endLine}:${witness.endColumn} · count ${witness.count}`,row,'muted');
    systemFlowLocation({fileID:witness.fileID,line:witness.startLine,endLine:witness.endLine},row,'Coverage witness');
  }
}
function systemFlowExchange(exchange,parent) {
  const byID=new Map(report.systemFlow.components.map(c=>[c.id,c]));
  const row=el('details',undefined,parent,'system-exchange');
  el('summary',(byID.get(exchange.from)?.name||exchange.from)+' → '+(byID.get(exchange.to)?.name||exchange.to)+' · '+exchange.kind,row);
  el('p','Static '+exchange.resolution+' relationship. '+(exchange.reason||''),row,'muted');
  for(const info of exchange.information||[])
    el('p',(info.direction==='output'?'Output contract':'Input contract')+': '+info.type+(info.name?' · '+info.name:''),row);
  if(['asynchronous','deferred'].includes(exchange.kind))
    el('p','Result signatures are not synchronous return delivery for go/defer calls.',row,'muted');
  for(const location of exchange.sources||[])systemFlowLocation(location,row);
}
function systemFlowDetail(scene) {
  const component=report.systemFlow.components.find(c=>c.id===mode.componentID);
  const operation=report.systemFlow.operations.find(op=>op.id===mode.operationID);
  detail(operation?.name||component?.name||'Architectural system flow',
    'Source-derived components and typed information exchanges. Arrows are not chronological sequence.');
  if(scene.error){el('p',scene.error,$('detail'),'error');return;}
  button('Collect selected unit tests',()=>$('run-tests').click(),$('detail'));
  if(component?.doc)el('p',component.doc,$('detail'));
  const declaration=operation?.symbolID&&sym(operation.symbolID);
  if(declaration?.doc)el('p',declaration.doc,$('detail'));
  if(operation) {
    systemFlowLocation(operation.location,$('detail'));
    if(declaration)button('Explore implementation',()=>go({kind:'logical',id:declaration.id}),$('detail'));
  }else if(component) {
    const sources=el('details',undefined,$('detail'));
    el('summary','Component source · '+component.sources.length,sources);
    systemFlowLazy(sources,()=>{for(const location of component.sources)systemFlowLocation(location,sources);});
  }
  const counts=new Map();
  for(const alternative of scene.alternatives)counts.set(alternative.execution,(counts.get(alternative.execution)||0)+1);
  el('p',[...counts].map(([state,count])=>count+' '+state).join(' · ')||'No relevant alternatives in this selection.',$('detail'),'muted');
  if(component) {
    el('h3','Relevant operation alternatives',$('detail'));
    if(scene.alternatives.length<=50||operation) {
      for(const alternative of scene.alternatives)systemFlowAlternative(alternative,$('detail'));
    }else{
      for(const op of report.systemFlow.operations.filter(op=>op.componentID===component.id)) {
        const alternatives=scene.alternatives.filter(a=>a.symbolID===op.symbolID);
        if(!alternatives.length)continue;
        const group=el('details',undefined,$('detail'));
        el('summary',op.name+' · '+systemFlowTools.executionState(alternatives)+' · '+alternatives.length+' alternatives',group);
        systemFlowLazy(group,()=>{for(const alternative of alternatives)systemFlowAlternative(alternative,group);});
      }
    }
  }else el('p','Choose a component group, then a component to inspect its meaningful operation alternatives. Supporting functions stay behind the component.',$('detail'));
  const contracts=el('details',undefined,$('detail'));
  el('summary','Broad information contracts · '+scene.exchanges.length,contracts);
  el('p','Types and source names describe contracts, not exact values or object lineage.',contracts,'muted');
  systemFlowLazy(contracts,()=>{for(const exchange of scene.exchanges)systemFlowExchange(exchange,contracts);});
  systemFlowProvenance($('detail'));
  const limits=el('details',undefined,$('detail'));el('summary','Inference & limits',limits);
  el('p','Package groups are structural navigation, not certified business roles. Mixed execution expands to the individual alternatives; unknown does not mean unexecuted.',limits);
  for(const text of scene.limitations)el('p',text,limits,'muted');
}
function renderSystemFlow() {
  const options=mode.kind==='application'?{level:'context'}:mode;
  const scene=systemFlowTools.project(report,options);
  $('list').replaceChildren();$('graph').replaceChildren();activeScene=null;
  updateCoverageSummary();
  const title=options.level==='context'?'System context':options.level==='operation'?'Operation alternatives':'Components & exchanges';
  const component=report.systemFlow.components.find(c=>c.id===mode.componentID);
  $('breadcrumb').textContent=report.module+' / '+title+(component?' / '+component.name:'');
  $('legend').textContent='SOURCE · → typed information contract · dashed = unresolved/inferred · not a time sequence';
  el('h3','Architectural views',$('list'));
  button('System context',()=>go({kind:'system-flow',level:'context'}),$('list'));
  button('Components & exchanges',()=>go({kind:'system-flow',level:'components'}),$('list'));
  const label=el('label','Find component group',$('list'));label.htmlFor='system-filter';
  const input=el('input',undefined,$('list'));input.id='system-filter';input.type='search';input.value=mode.query||'';
  input.oninput=()=>{
    const query=input.value,start=input.selectionStart,end=input.selectionEnd;
    mode={...mode,kind:'system-flow',level:options.level,query,viewport:undefined};render();
    $('system-filter').focus();$('system-filter').setSelectionRange(start,end);
  };
  if(options.group) {
    el('h3','Group components',$('list'));
    const components=report.systemFlow.components.filter(c=>c.package===options.group);
    for(const c of components) {
      const name=c.name+(components.some(other=>other.id!==c.id&&other.name===c.name)?' · '+c.kind:'');
      button(name,()=>go({kind:'system-flow',level:'operation',componentID:c.id,query:mode.query}),$('list'));
    }
  }
  if(component) {
    el('h3','Component operations',$('list'));
    button('All relevant alternatives',()=>go({...mode,operationID:undefined,viewport:undefined}),$('list'));
    for(const operation of report.systemFlow.operations.filter(op=>op.componentID===component.id))
      button(operation.name,()=>go({...mode,operationID:operation.id,viewport:undefined}),$('list'));
  }
  el('h3','Component groups',$('list'));
  for(const group of scene.groups.filter(g=>g.name.toLowerCase().includes((mode.query||'').toLowerCase())))
    button(group.name,()=>go({kind:'system-flow',level:'components',group:group.id,query:mode.query}),$('list'));
  button('HTTP endpoints & startup',()=>go({kind:'application',section:'endpoints'}),$('list'));
  button('Browse packages',()=>go({kind:'packages'}),$('list'));
  const catalog=options.level==='components'&&!options.group&&!options.componentID;
  if(catalog&&!scene.error) {
    const panel=$('application-overview');panel.replaceChildren();panel.hidden=false;
    $('graph').style.display='none';$('canvas').querySelector('.tools').hidden=true;document.querySelector('.graph-hint').hidden=true;
    el('h1','Choose a component group',panel);
    el('p','Explore one source capability and its neighbouring exchanges. The full catalog stays available without auto-fitting the entire implementation into one diagram.',panel,'muted');
    for(const group of scene.groups.filter(g=>g.name.toLowerCase().includes((mode.query||'').toLowerCase()))) {
      const item=button(group.name,()=>go({kind:'system-flow',level:'components',group:group.id,query:mode.query}),panel);
      el('span',' · '+group.members.length+' source components',item,'muted');
    }
    panel.scrollTop=mode.scroll||0;
  }else if(!scene.error&&scene.nodes.length) {
    const nodes=scene.nodes.map(({members,...node})=>({...node,architecture:true,
      label:(node.group?node.group.split('/').slice(-2).join('/'):node.name)+'\n'+members.length+' source component'+(members.length===1?'':'s'),
      status:node.alternatives.length?node.execution+' alternatives':'No alternative evidence'}));
    const edges=scene.edges.map(edge=>{
      const types=[...new Set(edge.information.map(i=>i.type))];
      const context=options.level==='context';
      const label=context&&types.length>2?
        (edge.direction==='output'?'Outputs':'Inputs')+' · '+types.length+' contracts':
        (edge.kind==='asynchronous'?'async · ':'')+(types.slice(0,2).map(t=>t.replace(/[\w./-]+\/(\w+)\.(\w+)/g,'$1.$2')).join(', ')||'contract unspecified')+(types.length>2?' +'+(types.length-2):'');
      const limit=context?36:65;
      return {...edge,architecture:true,indirect:edge.resolution!=='resolved'||edge.kinds.includes('asynchronous'),
        label:label.length>limit?label.slice(0,limit-3)+'...':label,
        description:types.join('\n')+' · '+edge.resolution+' · '+edge.kinds.join(', ')};
    });
    const viewport=mode.viewport;
    const focus=nodes.find(n=>n.id==='system'||n.id===mode.componentID)?.id||nodes[0].id;
    // Architectural neighbours have no temporal rank. Wrap their catalog
    // around the focus rather than making one very wide call-graph layer.
    const positions=options.level==='context'?new Map(nodes.map((n,i)=>[n.id,
      {slot:0,rank:n.kind==='actor'?0:n.id==='system'?1:i+2}])):
      new Map([[focus,{slot:nodes.length>1?1:0,rank:0}],...nodes.filter(n=>n.id!==focus).map((n,i)=>
        [n.id,{slot:i%3,rank:1+Math.floor(i/3)}])]);
    drawFlow(nodes,edges,focus,node=>{
      const original=scene.nodes.find(n=>n.id===node.id);
      mode={...mode,selected:node.id};
      if(original.id==='system')go({kind:'system-flow',level:'components'});
      else if(original.group)go({kind:'system-flow',level:'components',group:original.group,query:mode.query});
      else if(original.members.length===1)
        go({kind:'system-flow',level:'operation',componentID:original.members[0],query:mode.query});
      else{
        detail(original.name,'Grouped source boundaries, not runtime steps.');
        for(const id of original.members) {
          const c=report.systemFlow.components.find(c=>c.id===id);
          button(c.name+(c.package?' · '+c.package:''),()=>go({kind:'system-flow',level:'operation',componentID:id}),$('detail'));
        }
      }
    },positions);
    if(viewport){pan={...viewport};view();}
    else if(nodes.length<=5){pan={...activeScene.bounds};view();}
    if(mode.selected)$('graph').querySelectorAll('[data-node-id]').forEach(n=>n.classList.toggle('selected-step',n.dataset.nodeId===mode.selected));
  }
  systemFlowDetail(scene);
}
const beforeSystemRender=render,beforeSystemKey=graphKey,beforeSystemView=view,beforeSystemGo=go;
function architecturalMode(){return mode.kind==='system-flow';}
go=function(next,remember=true){
  if(report&&architecturalMode()&&!$('application-overview').hidden)mode={...mode,scroll:$('application-overview').scrollTop};
  beforeSystemGo(next,remember);
};
graphKey=function(){return architecturalMode()?'architecture:'+JSON.stringify([mode.level,mode.group,mode.componentID,mode.operationID,mode.query]):beforeSystemKey();};
view=function(){beforeSystemView();if(report&&architecturalMode())mode={...mode,viewport:{...pan}};};
render=function() {
  if(!report)return;
  const architecture=architecturalMode();
  document.body.classList.toggle('system-home',architecture);
  if(!architecture){beforeSystemRender();return;}
  document.body.classList.remove('application-home');
  $('application-overview').hidden=true;$('graph').style.display='';
  $('canvas').querySelector('.tools').hidden=false;document.querySelector('.graph-hint').hidden=false;
  for(const id of ['logical-flow','function-flow','data-flow','call-map','review-map'])$(id).setAttribute('aria-pressed','false');
  $('application').setAttribute('aria-pressed','true');$('compact-statements').disabled=true;
  renderSystemFlow();
};
