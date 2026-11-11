'use strict';
const applicationTools=GoReviewApplication;
const httpInventoryNote='Source registrations from net/http Handle and HandleFunc. Other routers, mounted prefixes, indirect registrations and startup reachability are not resolved.';

function applicationRows() {
  return applicationTools.entryInventory(report,{group:mode.group,query:mode.query});
}
function selectedEntryPoint(state=mode) {
  return applicationTools.entryByID(report,state.id);
}
function openEndpoint(row) {
  if(mode.kind==='application')mode={...mode,scroll:$('application-overview').scrollTop};
  go({kind:'endpoint',id:row.id,group:mode.group,query:mode.query,selected:row.handlerID});
}
function endpointTitle(endpoint) {
  return endpoint.patternKnown?endpoint.pattern||'(empty pattern)':endpoint.patternExpression;
}
function applicationFilter(parent) {
  const label=el('label','Find entry point',parent);label.htmlFor='endpoint-filter';
  const input=el('input',undefined,parent);input.id='endpoint-filter';input.type='search';
  input.placeholder='Name, interface, path or handler';input.value=mode.query||'';
  input.oninput=()=>{
    const value=input.value,start=input.selectionStart,end=input.selectionEnd;
    mode={...mode,query:value};render();
    $('endpoint-filter').focus();$('endpoint-filter').setSelectionRange(start,end);
  };
}
function applicationStartup(parent) {
  const entries=applicationTools.initializationInventory(report);
  el('h2','Supporting initialization',parent);
  el('p','Go init declarations support initialization; they are not external entry points.',parent,'muted');
  if(!entries.length)el('p','No init declarations found in the selected build.',parent);
  for(const row of entries) {
    const item=button(row.title,()=>openEndpoint(row),parent);
    item.title=row.title;item.className='startup-entry';item.dataset.symbolId=row.handlerID;
    visualMetrics(item,[row.handlerID]);
  }
}
function renderApplication() {
  $('list').replaceChildren();$('graph').replaceChildren();$('application-overview').replaceChildren();
  activeScene=null;updateCoverageSummary();
  $('breadcrumb').textContent=report.module+' / application';
  const all=applicationTools.entryInventory(report),rows=applicationRows();
  el('h3','Explore application',$('list'));
  button('All entry points · '+all.length,()=>{mode={...mode,group:undefined,section:undefined};render();},$('list'));
  const groups=new Map();
  for(const row of all)groups.set(row.group,(groups.get(row.group)||0)+1);
  for(const [group,count] of groups) {
    const item=button(group+' · '+count,()=>{mode={...mode,group,section:undefined};render();},$('list'));
    item.setAttribute('aria-pressed',String(mode.group===group));
  }
  button('Startup & wiring',()=>{mode={...mode,section:'startup'};render();},$('list'));
  if(report.systemFlow) {
    button('System context',()=>go({kind:'system-flow',level:'context'}),$('list'));
    button('Components & exchanges',()=>go({kind:'system-flow',level:'components'}),$('list'));
  }
  button('Browse packages',()=>go({kind:'packages'}),$('list'));
  const panel=$('application-overview');
  el('h1',mode.section==='startup'?'Supporting initialization':mode.group||
    ({wasm:'Wasm component exports',cli:'Program entry points',library:'Public library API',service:'Service entry points'}[report.entryPoints?.kind]||'Entry points'),panel);
  const purpose=el('section',undefined,panel,'entry-purpose');
  el('strong',report.module,purpose);
  const moduleDoc=report.packages.find(p=>p.path===report.module)?.doc;
  if(moduleDoc)el('p',moduleDoc,purpose);
  const overview=documentationTools.overviewDocument(report);
  if(overview)button('Project purpose & full documentation',()=>openDocumentation(overview),purpose);
  button(mode.section==='startup'?'Entry points':'Startup & wiring',()=>{
    mode={...mode,section:mode.section==='startup'?undefined:'startup'};render();
  },purpose);
  if(mode.section!=='startup')applicationFilter(purpose);
  if(mode.section==='startup') {
    applicationStartup(panel);
  } else {
    el('p',rows.length+' of '+all.length+' entry points · '+all.filter(r=>!!sym(r.handlerID)).length+' linked Go implementations',panel,'muted');
    if(!all.length) {
      el('h2','No external entry points resolved',panel);
      el('p','Missing entry evidence is not proof of no public behavior. Inspect discovery limits and retained contracts; analyze older snapshots again for WIT/configuration and public API discovery.',panel);
      applicationStartup(panel);
    } else if(!rows.length)el('p','No entry points match these filters.',panel);
    const groupsPanel=el('div',undefined,panel,'entry-groups');
    let group,groupPanel;
    for(const row of rows) {
      if(group!==row.group) {
        group=row.group;groupPanel=el('section',undefined,groupsPanel,'entry-group');
        el('h2',group,groupPanel,'endpoint-group-title');
        visualMetrics(groupPanel,rows.filter(r=>r.group===group).map(r=>r.handlerID).filter(Boolean));
        el('p','External entry declarations · not execution order',groupPanel,'muted');
      }
      const item=button('',()=>openEndpoint(row),groupPanel);item.className='endpoint-row';
      el('span',row.patternKnown?row.method:'?',item,'endpoint-method');
      el('span',row.patternKnown?(row.host+row.path)||'(empty pattern)':row.title,item,'endpoint-path');
      const handler=sym(row.handlerID);
      el('span',handler?logicalTitle(handler):'Implementation unresolved',item,'endpoint-handler');
      visualMetrics(item,[handler?.id||'unresolved-handler']);
      item.title=row.handler||row.reason||row.title;
    }
  }
  const limits=el('details',undefined,panel);el('summary','Detection scope',limits);
  if(!report.entryPoints)el('p','Older snapshot: analyze again to collect WIT, wasmCloud configuration and public library API entries.',limits,'error');
  for(const note of report.entryPoints?.limitations||[])el('p',note,limits);
  el('p',httpInventoryNote,limits);
  el('p','These are registration sites, not a live-server inventory. Duplicate patterns and dynamic patterns remain separate. ANY means the pattern has no method restriction; GET patterns also match HEAD in Go ServeMux.',limits);
  panel.scrollTop=mode.scroll||0;
}
function changeEndpoint(changes) {
  mode={...mode,...changes,viewport:undefined};render();
}
function endpointRegistrationDetail(endpoint) {
  detail(endpointTitle(endpoint),endpoint.entryKind==='http'?'HTTP registration':'Entry-point declaration');
  if(endpoint.handler)el('p',endpoint.handler,$('detail'));
  if(endpoint.reason)el('p',endpoint.reason,$('detail'),'data-boundary-note');
  if(!endpoint.patternKnown)el('p','The route pattern is computed at runtime; its value is unknown.',$('detail'),'data-boundary-note');
  const declaration=sym(endpoint.registrationID);
  if(declaration)button('Registration in '+logicalTitle(declaration),()=>go({kind:'logical',id:declaration.id}),$('detail'));
  if(endpoint.location)button('Inspect Go declaration',()=>source(endpoint.location),$('detail'));
  if(endpoint.api)el('p',endpoint.api+(endpoint.receiver?' · receiver '+endpoint.receiver:''),$('detail'),'muted');
  for(const evidence of endpoint.evidence||[]) {
    el('p',evidence.kind+(evidence.detail?' · '+evidence.detail:'')+
      (evidence.line?' · line '+evidence.line:''),$('detail'),'muted');
    const document=report.entryPoints?.sources?.find(d=>d.id===evidence.sourceID);
    if(document)button('Read '+document.path,()=>openDocumentation(document),$('detail'));
    if(evidence.location)button('Inspect binding source',()=>source(evidence.location),$('detail'));
  }
  if(endpoint.entryKind==='http')el('p',httpInventoryNote,$('detail'),'muted');
}

const beforeApplicationRender=render,beforeApplicationGo=go,beforeApplicationView=view,beforeApplicationKey=graphKey;
go=function(next,remember=true) {
  if(['symbol','logical','dataflow'].includes(next.kind)) {
    const context=mode.kind==='endpoint'?{...mode}:mode.endpointContext;
    if(context)next={...next,endpointContext:context};
  }
  beforeApplicationGo(next,remember);
};
view=function(){beforeApplicationView();if(mode.kind==='endpoint')mode={...mode,viewport:{...pan}};};
graphKey=function(){
  return mode.kind==='endpoint'?'walkthrough:'+mode.id+':'+JSON.stringify([mode.expanded,mode.compact]):beforeApplicationKey();
};
render=function() {
  if(!report)return;
  const home=mode.kind==='application',endpoint=mode.kind==='endpoint';
  document.body.classList.toggle('application-home',home);
  $('application-overview').hidden=!home;$('graph').style.display=home?'none':'';
  $('canvas').querySelector('.tools').hidden=home;
  document.querySelector('.graph-hint').hidden=home;
  $('application').setAttribute('aria-pressed',String(home||endpoint));
  if(home||endpoint) {
    for(const id of ['logical-flow','function-flow','data-flow','call-map','review-map'])$(id).setAttribute('aria-pressed','false');
    $('compact-statements').disabled=true;
    document.querySelector('.graph-hint').textContent='Scroll to pan · Shift+scroll or pinch to zoom · expand a call at its source';
    if(home)renderApplication();else renderEndpoint();
  } else {
    beforeApplicationRender();
    if(mode.endpointContext) {
      const context=mode.endpointContext,route=selectedEntryPoint(context);
      if(route) {
        const crumb=button('← '+endpointTitle(route),()=>go(context),undefined);
        $('breadcrumb').prepend(crumb);
      }
    }
  }
};
$('application').onclick=()=>go({kind:'application'});
for(const id of ['logical-flow','function-flow','data-flow','call-map']) {
  const previous=$(id).onclick;
  $(id).onclick=()=>{
    if(mode.kind!=='endpoint'){previous();return;}
    const route=selectedEntryPoint();
    if(!route){detail('Endpoint unavailable','Reopen the application overview for the current snapshot.');return;}
    const scene=mode.detailLevel==='function'?GoReviewWalkthrough.endpointWalkthrough(report,route,mode):overallEndpointScene;
    const node=scene?.nodes.find(n=>n.id===mode.selected);
    const selected=sym(node?.symbolID||node?.ownerID||route.handlerID);
    if(!selected){endpointRegistrationDetail(route);return;}
    if(id==='logical-flow')go({kind:'logical',id:selected.id});
    else if(id==='data-flow')go({kind:'dataflow',id:selected.id});
    else {preferControl=id==='function-flow';go({kind:'symbol',id:selected.id});}
  };
}
const beforeApplicationSearch=$('search').oninput;
$('search').oninput=()=>{
  if(['application','endpoint'].includes(mode.kind))go({kind:'packages'});
  else beforeApplicationSearch();
};
const beforeApplicationReset=$('reset-layout').onclick;
$('reset-layout').onclick=()=>{
  if(mode.kind==='endpoint')mode={...mode,viewport:undefined};
  beforeApplicationReset();
};
