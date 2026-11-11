'use strict';
const dataGraphs=new WeakMap();
const behaviorRender=render;
function dataEntry(snapshot,id) {
  let entries=dataGraphs.get(snapshot);
  if(!entries){entries=new Map();dataGraphs.set(snapshot,entries);}
  if(!entries.has(id))entries.set(id,{state:'new'});
  return entries.get(id);
}
async function loadDataFlow(snapshot,id,entry) {
  entry.state='loading';
  try {
    const response=await fetch('/api/dataflow',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({symbolID:id,sourceSHA256:snapshot.sourceSHA256})});
    const graph=await response.json();
    if(!response.ok)throw Error(graph.error||response.statusText);
    if(graph.symbolID!==id||graph.sourceSHA256!==snapshot.sourceSHA256)throw Error('Data-flow evidence does not match the selected source snapshot.');
    entry.graph=graph;entry.state='ready';
  }catch(error){entry.state='error';entry.error=error.message;}
  if(report===snapshot&&mode.kind==='dataflow'&&mode.id===id)render();
}
function dataComments(s,node) {
  if(!node.location)return [];
  return (s.comments||[]).filter(comment=>
    comment.location.fileID===node.location.fileID&&
    comment.location.line<=node.location.endLine&&
    comment.location.endLine>=node.location.line-1);
}
function renderDataFlow() {
  const s=sym(mode.id);
  $('list').replaceChildren();$('graph').replaceChildren();updateCoverageSummary();
  $('function-flow').setAttribute('aria-pressed','false');
  $('call-map').setAttribute('aria-pressed','false');
  if(!s){detail('Data flow unavailable','The selected symbol is not in this source snapshot.');return;}
  $('breadcrumb').textContent=short(s.package)+' / '+s.name+' / value data flow';
  $('legend').textContent='STATIC SSA · definition → operand use · orange outline = boundary';
  const entry=dataEntry(report,s.id);
  if(entry.state==='new'){loadDataFlow(report,s.id,entry);}
  if(entry.state!=='ready') {
    detail(entry.state==='error'?'Data flow unavailable':'Collecting value data flow',
      entry.state==='error'?entry.error:'Building source-linked SSA using the local Go analysis toolchain. Target tests are not executed.');
    if(entry.state==='error')button('Retry data-flow analysis',()=>{entry.state='new';render();},$('detail'));
    unitEvidence(s);
    return;
  }
  const graph=entry.graph;
  if(!graph.nodes.length){detail('No SSA values','No instructions or parameters were emitted for this function.');return;}
  const focus=graph.nodes.find(n=>n.id===mode.value)||graph.nodes.find(n=>n.kind==='return')||graph.nodes[0];
  const direction=mode.direction||'both', depth=mode.depth===undefined?1:mode.depth;
  const subset=mode.all?graph:GoReviewDataFlow.slice(graph,focus.id,direction,depth);
  const nodes=subset.nodes.map(node=>({...node,reachable:true,comments:dataComments(s,node)}));
  const positions=GoReviewDataFlow.positions(subset,focus.id);
  selectedStep=focus.id;
  drawFlow(nodes,subset.edges,focus.id,node=>go({...mode,value:node.id,all:false}),positions);
  focusGraphNode(focus.id);
  const groups=new Map([...$('graph').querySelectorAll('.flow-node')].map(element=>[element.dataset.nodeId,element]));
  for(const node of nodes) {
    const group=groups.get(node.id);
    if(group&&node.boundary)group.classList.add('data-boundary');
    const callee=node.calleeID?sym(node.calleeID):null;
    if(group&&callee) {
      svg('text',{x:308,y:20,'text-anchor':'end',class:'callee-link'},group).textContent='↗ '+callee.name.slice(0,24);
      svg('title',{},group).textContent='Known call target: '+callee.package+'.'+callee.name+' · select to explore';
    }
  }
  el('h3','Values and operations',$('list'));
  const search=el('input',undefined,$('list'));
  search.type='search';search.placeholder='Filter values, types, boundaries';
  search.setAttribute('aria-label','Filter data-flow values');
  const valueList=el('div',undefined,$('list'));
  const showValues=()=>{
    valueList.replaceChildren();
    const query=search.value.toLowerCase(), matches=graph.nodes.filter(n=>(n.label+' '+n.type+' '+(n.boundary||'')).toLowerCase().includes(query));
    el('p',matches.length+' matching values / operations',valueList,'muted');
    for(const node of matches)button(node.kind+' · '+node.label,()=>go({...mode,value:node.id,all:false}),valueList);
  };
  search.oninput=showValues;showValues();
  detail(s.name+' · '+focus.kind,focus.label);
  el('pre',focus.type||'No SSA result type (effect or control instruction)',$('detail'));
  el('p',`${nodes.length} of ${graph.nodes.length} SSA nodes shown · ${mode.all?'whole function':direction+' · '+(depth===Infinity?'all levels':depth+' level(s)')}`,$('detail'),'muted');
  metricGauge($('detail'),'coverage',s.location);
  metricGauge($('detail'),'complexity',null,s.cyclomatic);
  unitEvidence(s);
  if(focus.boundary)el('p',focus.boundary,$('detail'),'data-boundary-note');
  const callee=focus.calleeID?sym(focus.calleeID):null;
  if(callee) {
    button('↗ Control flow · '+callee.name,()=>{preferControl=true;go({kind:'symbol',id:callee.id});},$('detail'));
    button('↗ Data flow · '+callee.name,()=>go({kind:'dataflow',id:callee.id}),$('detail'));
  }
  button('Direct dependencies & uses',()=>go({...mode,value:focus.id,direction:'both',depth:1,all:false}),$('detail'));
  button('Trace upstream definitions',()=>go({...mode,value:focus.id,direction:'upstream',depth:Infinity,all:false}),$('detail'));
  button('Trace downstream uses',()=>go({...mode,value:focus.id,direction:'downstream',depth:Infinity,all:false}),$('detail'));
  button('Expand one level',()=>go({...mode,value:focus.id,depth:depth===Infinity?Infinity:depth+1,all:false}),$('detail'));
  button('Show whole function',()=>go({...mode,value:focus.id,all:true}),$('detail'));
  for(const [title,edges,other] of [
    ['Defined from',graph.edges.filter(e=>e.to===focus.id),'from'],
    ['Used by',graph.edges.filter(e=>e.from===focus.id),'to']
  ]) {
    el('h3',title,$('detail'));
    if(!edges.length)el('p','No direct SSA operand edges in this direction. Memory, external calls and other functions are not traversed.',$('detail'),'muted');
    for(const edge of edges) {
      const target=graph.nodes.find(n=>n.id===edge[other]);
      if(target)button(edge.label+' → '+target.label,()=>go({...mode,value:target.id,all:false}),$('detail'));
    }
  }
  if($('show-comments').checked)for(const comment of dataComments(s,focus))el('p','Nearby source comment: '+comment.text,$('detail'),'step-comment');
  if(focus.location){source(focus.location);const snippet=$('detail').lastElementChild;if(snippet?.tagName==='DETAILS')snippet.open=false;}
  else el('p','The SSA tool emitted no exact source position for this node. Use the connected source-linked operation for context.',$('detail'),'muted');
  const docs=el('details',undefined,$('detail'));
  el('summary','Function documentation & analysis boundaries',docs);
  el('p','Arrows are direct SSA operand dependencies. A phi node merges possible incoming definitions; a chain is not proof of an executable path.',docs,'muted');
  el('p',s.doc||'No Go documentation on this declaration.',docs);
  for(const text of graph.limitations)el('p',text,docs,'muted');
  el('p',(graph.collector||'golang.org/x/tools/go/ssa')+' · '+(graph.toolVersion||'')+' · '+(graph.generatedAt||''),docs,'muted');
  el('p','Snapshot SHA-256: '+graph.sourceSHA256,docs,'muted');
  for(const command of graph.commands||[]) {
    el('pre',[command.executable,...command.args].join(' '),docs);
    el('p','Exit '+command.exitCode+' · '+command.directory,docs,'muted');
    if(command.stderr)el('pre',command.stderr,docs);
  }
}
render=function() {
  if(!report)return;
  $('data-flow').setAttribute('aria-pressed',String(mode.kind==='dataflow'));
  if(mode.kind==='dataflow'){$('compact-statements').disabled=true;renderDataFlow();}
  else behaviorRender();
};
$('data-flow').onclick=()=>{
  if(mode.kind==='symbol'||mode.kind==='dataflow')go({kind:'dataflow',id:mode.id});
  else detail('Select a function first','Choose a function in a package or search result, then select Value data flow.');
};
for(const [id,control] of [['function-flow',true],['call-map',false]]) {
  const previous=$(id).onclick;
  $(id).onclick=()=>{
    if(mode.kind==='dataflow'){preferControl=control;go({kind:'symbol',id:mode.id});}
    else previous();
  };
}
const behaviorSearch=$('search').oninput;
$('search').oninput=()=>{
  if(mode.kind==='dataflow'&&$('search').value.trim())go({kind:'packages'});
  else behaviorSearch();
};
const behaviorFit=$('fit').onclick;
$('fit').onclick=()=>{
  if(mode.kind==='dataflow'){
    if(activeScene&&$('graph').querySelector('.node')){pan={...activeScene.bounds};view();}
  }else behaviorFit();
};
