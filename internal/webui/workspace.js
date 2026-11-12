'use strict';
let workspaceRendering=false;
const beforeWorkspaceView=view;
view=function() {
  beforeWorkspaceView();
  if(!workspaceRendering&&report&&!['application','endpoint'].includes(mode.kind))
    mode={...mode,workspaceViewport:{...pan}};
};

function workspaceCallable() {
  if(['symbol','logical','dataflow','review','observed'].includes(mode.kind))return sym(mode.id);
  if(mode.kind==='endpoint') {
    const route=selectedEntryPoint();
    const scene=mode.detailLevel==='function'&&route?GoReviewWalkthrough.endpointWalkthrough(report,route,mode):overallEndpointScene;
    const node=scene?.nodes.find(n=>n.id===mode.selected);
    return sym(node?.symbolID||node?.ownerID||route?.handlerID);
  }
  if(mode.kind==='system-flow'&&mode.operationID)
    return sym(report.systemFlow?.operations.find(o=>o.id===mode.operationID)?.symbolID);
  return null;
}

function workspaceCapture() {
  return {list:$('list').scrollTop,detail:$('detail').scrollTop,
    overview:$('application-overview').scrollTop,pan:{...pan}};
}

function workspaceRestore(state) {
  if(!state)return;
  $('list').scrollTop=state.list;
  $('detail').scrollTop=state.detail;
  $('application-overview').scrollTop=state.overview;
  if(state.pan&&$('graph').style.display!=='none'){pan={...state.pan};view();}
}

const beforeWorkspaceGo=go;
go=function(next,remember=true) {
  if(remember&&!['application','endpoint'].includes(mode.kind))
    mode={...mode,workspaceState:workspaceCapture(),workspaceControl:preferControl};
  beforeWorkspaceGo(next,remember);
  workspaceRestore(next.workspaceState);
};

function workspaceAncestor(target) {
  const index=history.findLastIndex(item=>item.kind===target.kind&&
    (target.id===undefined||item.id===target.id)&&
    (target.group===undefined||item.group===target.group));
  if(index<0){go(target);return;}
  mode=history[index];
  history.splice(index);
  if(mode.workspaceControl!==undefined)preferControl=mode.workspaceControl;
  render();workspaceRestore(mode.workspaceState);
}

function workspaceBreadcrumbs() {
  // The Application flow owns its entry/parent context; keep that renderer intact.
  if(['application','endpoint'].includes(mode.kind)&&!mode.documentOpen)return;
  const parent=$('breadcrumb'),symbol=workspaceCallable(),crumbs=[];
  crumbs.push({title:'Application',target:{kind:'application'}});
  if(mode.kind==='system-flow') {
    crumbs.push({title:'Components',target:{kind:'system-flow',level:'components'}});
    const component=report.systemFlow?.components.find(c=>c.id===mode.componentID);
    const group=mode.group||component?.package;
    if(group)crumbs.push({title:short(group),target:{kind:'system-flow',level:'components',group}});
    if(component)crumbs.push({title:component.name});
    if(symbol)crumbs.push({title:label(symbol)});
    if(mode.level==='context')crumbs.push({title:'System context'});
  } else if(mode.kind==='signals') {
    crumbs.push({title:mode.section==='hygiene'?'Checks & hygiene':'Architecture signals',
      target:{kind:'signals',section:mode.section}});
    if(mode.package)crumbs.push({title:short(mode.package)});
  } else {
    crumbs.push({title:'Packages',target:{kind:'packages'}});
    const path=mode.kind==='package'?mode.id:symbol?.package;
    if(path)crumbs.push({title:short(path),target:{kind:'package',id:path}});
    if(symbol)crumbs.push({title:label(symbol),target:{kind:'logical',id:symbol.id}});
    const view=mode.kind==='dataflow'?'Data flow':mode.kind==='symbol'?
      (preferControl?'Control flow':'Call relationships'):mode.kind==='review'?'Review map':
      mode.kind==='logical'?'Logical flow':null;
    if(view)crumbs.push({title:view});
  }
  if(mode.documentOpen)crumbs.push({title:'Documentation'});
  parent.replaceChildren();parent.setAttribute('aria-label','Review location');
  crumbs.forEach((crumb,i)=>{
    if(i)el('span','›',parent,'breadcrumb-separator').setAttribute('aria-hidden','true');
    if(crumb.target&&i<crumbs.length-1)
      button(crumb.title,()=>workspaceAncestor(crumb.target),parent);
    else el('span',crumb.title,parent).setAttribute('aria-current','page');
  });
}

function workspaceFunctionViews() {
  const symbol=workspaceCallable();
  const callable=!!symbol&&['function','method','closure','test'].includes(symbol.kind);
  const control=callable&&!!flowOf(symbol.id);
  const active=callable?(mode.kind==='logical'?'logical-flow':mode.kind==='dataflow'?'data-flow':
    mode.kind==='symbol'?(preferControl&&control?'function-flow':'call-map'):null):null;
  $('function-view-hint').textContent=callable?'Function: '+label(symbol):'Select a function to inspect its behavior';
  for(const id of ['logical-flow','function-flow','data-flow','call-map']) {
    $(id).disabled=!callable||(id==='function-flow'&&!control);
    $(id).setAttribute('aria-pressed',String(id===active));
    $(id).title=!callable?'Select a function from a package, search result, or component operation first.':
      id==='function-flow'&&!control?'No source control-flow graph is available for '+label(symbol)+'.':'Inspect '+label(symbol);
  }
  $('review-map').disabled=!report;
  $('review-map').setAttribute('aria-pressed',String(mode.kind==='review'));
  $('review-map').title='Compare coverage and complexity across repository functions.';
}

const beforeWorkspaceCollect=collect;
collect=async function(...args) {
  try {return await beforeWorkspaceCollect(...args);}
  finally {workspaceFunctionViews();}
};

function workspaceExplorer() {
  const symbol=workspaceCallable(),path=mode.kind==='package'?mode.id:mode.kind==='symbol'?symbol?.package:null;
  if(!path||$('search').value.trim())return;
  const parent=$('list');parent.replaceChildren();
  button('← All packages',()=>workspaceAncestor({kind:'packages'}),parent);
  const input=el('input',undefined,parent);
  input.type='search';input.placeholder='Filter functions';input.value=mode.functionFilter||'';
  input.setAttribute('aria-label','Filter package functions');
  const entries=el('div',undefined,parent);
  const functions=report.symbols.filter(s=>s.package===path&&['function','method','closure'].includes(s.kind));
  const populate=()=>{
    entries.replaceChildren();
    const query=input.value.toLowerCase(),visible=functions.filter(s=>(label(s)+' '+s.doc).toLowerCase().includes(query));
    el('p',visible.length+' of '+functions.length+' functions',entries,'muted');
    for(const s of visible) {
      const item=button(label(s),()=>go({kind:'logical',id:s.id}),entries);
      item.dataset.callableId=s.id;item.title=s.signature||s.qualified||label(s);
      if(s.id===symbol?.id)item.setAttribute('aria-current','true');
    }
    if(!visible.length)el('p',query?'No functions match this filter.':'No reviewable functions in this package.',entries,'muted');
  };
  input.oninput=()=>{mode={...mode,functionFilter:input.value};populate();};
  populate();
}

function workspaceInspector() {
  const detail=$('detail');
  if(!document.body.classList.contains('review-inspector-open'))return;
  detail.querySelector('.inspector-actions')?.remove();
  const actions=el('div',undefined,undefined,'inspector-actions');
  el('span',document.body.classList.contains('workspace-reader')?'Evidence reader':'Selected details',actions);
  button('Close details',()=>{
    mode={...mode,inspectorOpen:false,workspacePanel:undefined,documentOpen:false};
    document.body.classList.remove('review-inspector-open','workspace-reader');
    $('workspace-evidence').setAttribute('aria-pressed','false');
    $('workspace-evidence').focus();
  },actions);
  detail.prepend(actions);
}

function workspaceNavigation() {
  const current=typeof qualityState!=='undefined'&&qualityState.active?'test-quality':typeof changeState!=='undefined'&&changeState.active?'change-review':mode.documentOpen?'docs':mode.kind==='review'?'review-map':['application','endpoint'].includes(mode.kind)?'application':mode.kind==='packages'||mode.kind==='package'?'packages':
    mode.kind==='system-flow'?(mode.level==='context'?'system-context':'system-components'):
    mode.kind==='signals'?(mode.section==='hygiene'?'hygiene':'architecture-signals'):null;
  for(const b of document.querySelectorAll('.review-sections button')) {
    if(b.id===current)b.setAttribute('aria-current','page');else b.removeAttribute('aria-current');
  }
  $('application').setAttribute('aria-pressed',String(current==='application'));
  $('back').disabled=history.length===0;
  $('workspace-evidence').setAttribute('aria-pressed',String(document.body.classList.contains('review-inspector-open')));
}

const beforeWorkspaceRender=render;
render=function() {
  if(!report){beforeWorkspaceRender();return;}
  if(mode.workspacePanel==='docs'&&!mode.documentOpen)mode={...mode,workspacePanel:undefined};
  workspaceRendering=true;
  try {beforeWorkspaceRender();} finally {workspaceRendering=false;}
  if(['packages','package'].includes(mode.kind)&&activeScene) {
    if(mode.workspaceViewport)pan={...mode.workspaceViewport};
    else if($('graph').clientWidth&&$('graph').clientHeight) {
      // Start large structural maps at readable node size; Fit all is opt-in.
      const width=Math.min(activeScene.bounds.w,$('graph').clientWidth/.85);
      pan={x:activeScene.bounds.x,y:activeScene.bounds.y,w:width,h:width*$('graph').clientHeight/$('graph').clientWidth};
    }
    workspaceRendering=true;
    try {view();} finally {workspaceRendering=false;}
  }
  $('fit').textContent='Fit all';
  // An explicit false must survive legacy renderers that always opened details.
  if(mode.inspectorOpen===false)document.body.classList.remove('review-inspector-open');
  const reader=(mode.documentOpen||mode.workspacePanel)&&mode.inspectorOpen!==false;
  document.body.classList.toggle('workspace-reader',!!reader);
  workspaceBreadcrumbs();workspaceFunctionViews();workspaceNavigation();workspaceInspector();workspaceExplorer();
  if(!document.body.classList.contains('review-diagram')) {
    const heading=el('h2',mode.kind==='signals'?'Packages & findings':mode.kind==='system-flow'?'Components':
      mode.kind==='dataflow'?'Values & operations':'Packages & functions',undefined,'explorer-heading');
    $('list').prepend(heading);
  }
};

$('back').onclick=()=>{
  if(!history.length)return;
  mode=history.pop();
  if(mode.workspaceControl!==undefined)preferControl=mode.workspaceControl;
  render();workspaceRestore(mode.workspaceState);
};

for(const [id,kind] of [['logical-flow','logical'],['function-flow','symbol'],['data-flow','dataflow'],['call-map','symbol'],['review-map','review']]) {
  $(id).onclick=()=>{
    const symbol=workspaceCallable();
    if(kind!=='review'&&(!symbol||!['function','method','closure','test'].includes(symbol.kind))){
      status('Select a function from a package, search result, or component operation first.',true);return;
    }
    const previousControl=preferControl;
    const endpoint=mode.kind==='endpoint';
    if(kind==='symbol')preferControl=id==='function-flow';
    go({kind,id:symbol?.id,inspectorOpen:true});
    if(history.length&&!endpoint)history.at(-1).workspaceControl=previousControl;
  };
}

for(const id of ['docs','review-results','scope-results','evidence','run-tests','import-coverage']) {
  const previous=$(id).onclick;
  $(id).onclick=event=>{
    if(!report){previous?.(event);return;}
    mode={...mode,workspacePanel:id,inspectorOpen:true};
    document.body.classList.add('review-inspector-open','workspace-reader');
    previous?.(event);
    workspaceNavigation();workspaceInspector();
    $('detail').scrollTop=0;
  };
}

const beforeWorkspaceEvidence=$('workspace-evidence').onclick;
$('workspace-evidence').onclick=()=>{
  beforeWorkspaceEvidence();
  document.body.classList.toggle('workspace-reader',!!(mode.documentOpen||mode.workspacePanel)&&!!mode.inspectorOpen);
  workspaceInspector();
};

document.addEventListener('keydown',event=>{
  if(event.key!=='Escape'||!document.body.classList.contains('review-inspector-open')||
    /INPUT|TEXTAREA|SELECT/.test(event.target.tagName)||event.target.isContentEditable)return;
  const close=$('detail').querySelector('.inspector-actions button');
  close?.click();
});

document.addEventListener('click',event=>{
  if(!event.target.closest('#graph .node')||['application','endpoint'].includes(mode.kind))return;
  mode={...mode,inspectorOpen:true};
  document.body.classList.add('review-inspector-open');
  $('workspace-evidence').setAttribute('aria-pressed','true');
  workspaceInspector();
},true);
