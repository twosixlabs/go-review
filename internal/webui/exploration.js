'use strict';
const flowTools=GoReviewFlow;
let preferControl=true, selectedStep='', flowBounds=null;
const previousRender=render;
function unitEvidence(s,parent=$('detail')) {
  const section=el('details',undefined,parent,'unit-evidence');
  el('summary','Architectural test evidence',section);
  const alternatives=(report.systemFlow?.alternatives||[]).filter(a=>a.symbolID===s.id);
  for(const a of alternatives) {
    el('p',`${a.decision} · ${a.label} · ${a.execution}`,section,'badge');
    el('p',a.reason,section,'muted');
  }
  if(!report.unitEvidence)el('p','No selected unit-test execution loaded. Browsing never runs tests.',section,'muted');
  for(const run of report.unitEvidence?.runs||[]) {
    el('p',`${run.selection.package} · ${run.matchedTests.join(', ')||'no matched tests'} · ${run.status}`,section);
    if(run.error)el('pre',run.error,section,'error');
  }
  el('p','Coverage supports path inference for the selection, not a function-call sequence or per-test data lineage.',section,'muted');
}

function flowOf(id) { return (report.controlFlows||[]).find(f=>f.symbolID===id); }
function coverageBadge(location,parent) {
  const c=flowTools.coverageFor(location,report.coverage);
  metricGauge(parent,'coverage',location);
  return c;
}
function updateCoverageSummary() {
  const coverage=report.coverage;
  $('coverage-summary').replaceChildren();
  const m=coverage&&coverage.statements?coverage.coveredStatements/coverage.statements*100:null;
  const summary=el('span',m===null?'Coverage ?':`Coverage ${m.toFixed(1)}%${coverage.sourceVerified?'':' · unverified'}`,$('coverage-summary'),'badge');
  summary.title=coverage?'Profile-wide statement coverage · '+(coverage.sourceVerified?'source verified':'freshness unverified'):'No coverage loaded';
}
function summaryLines(text) {
  const lines=String(text||'').trim().split('\n').filter(Boolean);
  const wrapped=lines.flatMap(line=>{
    const result=[];
    for(let i=0;i<Math.min(line.length,150);i+=40) result.push(line.slice(i,i+40));
    return result;
  });
  return wrapped.slice(0,3).map((text,i)=>i===2&&wrapped.length>3?text.slice(0,37)+'...':text);
}
function drawFlow(nodes,edges,entry,clickNode,positions) {
  const root=$('graph');root.replaceChildren();
  const placed=positions||flowTools.ranks(nodes,edges,entry);
  const architecture=nodes.every(n=>n.architecture);
  const coords=new Map(nodes.map(n=>{const p=placed.get(n.id);return [n.id,{x:40+p.slot*(architecture?460:380),y:35+p.rank*(architecture?250:180)}];}));
  const W=320,H=142;
  nodes.forEach(node=>{
    const location=node.locations||node.location;
    const p=coords.get(node.id),cov=flowTools.coverageFor(location,report.coverage).state;
    const kind=node.members?'▸ '+node.members.length+' statements':node.kind;
    const group=svg('g',{class:'node flow-node cov-'+cov+(node.members?' statement-group':'')+(node.reachable===false?' unreachable':'')+(selectedStep===node.id?' selected-step':''),'data-node-id':node.id,transform:`translate(${p.x},${p.y})`,tabindex:0,role:'button','aria-label':node.members?kind+': '+node.label:node.label||node.kind},root);
    svg('rect',{width:W,height:H,rx:['entry','exit','return'].includes(node.kind)?32:8},group);
    svg('text',{x:12,y:20,class:'step-kind'},group).textContent=kind+(node.reachable===false?' · unreachable in this CFG':'');
    summaryLines(node.label).forEach((line,i)=>{svg('text',{x:12,y:42+i*16},group).textContent=line;});
    const comment=$('show-comments').checked?(node.comments||[])[0]:null;
    svg('text',{x:12,y:94,class:'sub'},group).textContent=node.architecture?node.status:comment?
      '// '+comment.text.trim().split('\n')[0].slice(0,43):
      node.location?.line?`line ${node.location.line}${node.members?'–'+node.location.endLine:''} · ${cov==='unknown'?'no coverage evidence':cov}`:'';
    const owner=mode.kind==='symbol'||mode.kind==='dataflow'?sym(mode.id):node.symbol||node.owner||sym(node.ownerID);
    if(!node.architecture&&(mode.kind!=='logical'||node.symbol)&&(mode.kind!=='endpoint'||location))
      nodeGauges(group,location,['observed','logical','endpoint'].includes(mode.kind)||node.kind==='entry'?owner?.cyclomatic:undefined,124,W);
    if(!node.architecture)nodeSignalIndicators(group,{symbolID:owner?.id,location:node.location},177,118);
    svg('title',{},group).textContent=(node.members?node.members.length+' statements · select to expand\n':'')+node.label+'\n'+(node.comments||[]).map(c=>c.text).join('\n');
    const activate=()=>{selectedStep=node.id;root.querySelectorAll('.selected-step').forEach(n=>n.classList.remove('selected-step'));group.classList.add('selected-step');clickNode(node);};
    group.onclick=activate;
    group.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();activate();}};
  });
  const scene=attachGraph(coords,edges,W,H,graphKey());
  flowBounds=scene.bounds;
  const first=coords.get(entry);
  pan={x:first?Math.max(0,first.x-30):0,y:0,w:Math.min(650,flowBounds.w),h:Math.min(700,flowBounds.h)};
  view();
}

function toggleStatementGroup(group,expand,step) {
  const expanded=new Set(mode.expandedStatements||[]);
  if(expand)expanded.add(group.id);else expanded.delete(group.id);
  mode={...mode,expandedStatements:[...expanded],step:step||(expand?group.members[0].id:group.id)};
  render();focusGraphNode(mode.step);
}

function stepDetail(flow,node) {
  const s=sym(flow.symbolID);
  detail(`${s?s.name:'Function'} · ${node.members?node.members.length+' statements':node.kind}`,node.members?'':node.label);
  el('span','STATIC',$('detail'),'badge');
  coverageBadge(node.locations||node.location,$('detail'));
  if(s)unitEvidence(s);
  if(node.members) {
    button('Expand '+node.members.length+' statements',()=>toggleStatementGroup(node,true),$('detail'));
    const unknown=node.members.filter(m=>flowTools.coverageFor(m.location,report.coverage).state==='unknown').length;
    if(unknown)el('p',`${unknown}/${node.members.length} steps have no matching coverage evidence.`,$('detail'),'muted');
    for(const member of node.members) {
      const item=el('details',undefined,$('detail'));
      el('summary','Line '+member.location.line+' · '+member.label.slice(0,55),item);
      el('pre',member.label,item);coverageBadge(member.location,item);
      (member.comments||[]).forEach(c=>el('p',c.text,item,'step-comment'));
      button('Select this statement',()=>toggleStatementGroup(node,true,member.id),item);
    }
  } else {
    (node.comments||[]).forEach(c=>el('p',c.text,$('detail'),'step-comment'));
    const group=flow.groups?.find(g=>g.members.some(m=>m.id===node.id));
    if(group)button('Collapse '+group.members.length+' statements',()=>toggleStatementGroup(group,false),$('detail'));
  }
  const next=flow.edges.filter(e=>e.from===node.id);
  if(next.length) {
    el('h3','Follow possible next step',$('detail'));
    next.forEach(edge=>{
      const target=flow.nodes.find(n=>n.id===edge.to);
      if(target) button((edge.label||'next')+' → '+target.label.slice(0,65),()=>{
        mode={...mode,step:target.id};render();
        focusGraphNode(target.id);
      },$('detail'));
    });
  }
  const calls=(node.callIDs||[]).map(id=>report.calls.find(c=>c.id===id)).filter(Boolean);
  if(calls.length) {
    el('h3','Calls at this step',$('detail'));
    calls.forEach(c=>button(c.target+' · '+c.kind,()=>{
      if(c.to)go({kind:'symbol',id:c.to});
      else {detail(c.target,c.reason||'No local call target.');source(c.location);}
    },$('detail')));
  }
  if(node.location){source(node.location);const snippet=$('detail').lastElementChild;if(snippet?.tagName==='DETAILS')snippet.open=false;}
}

function controlFlow(s) {
  const flow=flowOf(s.id);
  if(!flow) {
    el('p','No function control-flow graph for this symbol. Select a function with a body.',$('detail'));
    return;
  }
  $('breadcrumb').textContent=short(s.package)+' / '+s.name+' / control flow';
  $('legend').textContent='STATIC · → possible next step · dashed ↶ loop';
  const compact=$('compact-statements').checked;
  const visible=compact?flowTools.compactFlow(flow,new Set(mode.expandedStatements||[])):{...flow,groups:[]};
  const nodes=visible.nodes.filter(n=>$('show-unreachable').checked||n.reachable!==false);
  const selected=nodes.find(n=>n.id===mode.step||n.members?.some(m=>m.id===mode.step))||
    nodes.find(n=>mode.step?.startsWith('statements:')&&n.id===mode.step.slice('statements:'.length));
  selectedStep=selected?.id||'';
  drawFlow(nodes,visible.edges,visible.entry,node=>{mode={...mode,step:node.id};stepDetail(visible,node);});
  if(selected){stepDetail(visible,selected);return;}
  detail(s.name);
  coverageBadge(s.location,$('detail'));
  metricGauge($('detail'),'complexity',null,s.cyclomatic);
  unitEvidence(s);
  const steps=flow.nodes.filter(n=>$('show-unreachable').checked||n.reachable!==false).length;
  el('p',`${nodes.length} nodes · ${steps} steps`,$('detail'),'muted');
  button('Start at entry',()=>{
    mode={...mode,step:flow.entry};render();focusGraphNode(flow.entry);
  },$('detail'));
  button('Inspect function source',()=>source(s.location),$('detail'));
  const docs=el('details',undefined,$('detail'));el('summary','Documentation',docs);
  el('p',s.doc||'No Go documentation on this declaration.',docs);
  el('pre',s.signature,docs);
  const limits=el('details',undefined,$('detail'));el('summary','Analysis boundaries',limits);
  (flow.limitations||[]).forEach(text=>el('p',text,limits,'muted'));
}

render=function() {
  if(!report)return;
  $('compact-statements').disabled=mode.kind!=='symbol'||!preferControl;
  if(mode.kind==='observed')mode={kind:'packages'};
  previousRender();
  updateCoverageSummary();
  if(mode.kind==='symbol') {
    const s=sym(mode.id);
    if(s&&preferControl)controlFlow(s);
    else if(s&&['function','method','closure','test'].includes(s.kind))unitEvidence(s);
  }
  $('function-flow').setAttribute('aria-pressed',String(preferControl&&mode.kind==='symbol'));
  $('call-map').setAttribute('aria-pressed',String(!preferControl&&mode.kind==='symbol'));
};
$('function-flow').onclick=()=>{preferControl=true;render();};
$('call-map').onclick=()=>{preferControl=false;render();};
$('show-comments').onchange=()=>render();
$('show-unreachable').onchange=()=>render();
$('compact-statements').onchange=()=>render();
$('search').oninput=()=>render();
$('fit').onclick=()=>{
  if(activeScene&&$('graph').querySelector('.node')){pan={...activeScene.bounds};view();}
};

async function collect(url,body,message) {
  status(message);document.querySelectorAll('.assessment button').forEach(b=>b.disabled=true);
  try {
    const response=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
    const result=await response.json();
    if(!response.ok)throw Error(result.error||response.statusText);
    report=result;render();
    status(result.warnings?.length?`Evidence collected with ${result.warnings.length} warnings. Open Evidence & limits.`:'Evidence collected. Source-linked results are ready.');
    return true;
  }catch(error){status(error.message,true);return false;}
  finally{document.querySelectorAll('.assessment button').forEach(b=>b.disabled=false);}
}

$('import-coverage').onclick=()=>{
  if(!report){status('Analyze a Go module first.',true);return;}
  detail('Import statement coverage','Load an existing standard Go coverage profile. The tool preserves exact block ranges. An imported profile cannot establish that its source revision matches this snapshot; it is labeled unverified.');
  const form=el('form',undefined,$('detail'),'evidence-form');
  const input=el('input',undefined,form);input.required=true;input.placeholder='/absolute/path/coverage.out';input.setAttribute('aria-label','Coverage profile path');
  el('button','Load coverage',form);
  form.onsubmit=async e=>{e.preventDefault();await collect('/api/coverage',{path:input.value},'Loading coverage blocks…');};
};

$('run-tests').onclick=()=>{
  if(!report){status('Analyze a Go module first.',true);return;}
  detail('Collect selected unit-test evidence','Each exact selection runs once using standard Go coverage. Package initialization, dependencies and TestMain may execute and perform I/O. Select only unit tests you have inspected and trust; no application, integration, smoke or benchmark selections.');
  const form=el('form',undefined,$('detail'),'evidence-form');
  el('label','Exact package',form);
  const packages=el('input',undefined,form);packages.required=true;packages.setAttribute('aria-label','Unit-test package');
  const current=sym(mode.id);
  const component=report.systemFlow?.components.find(c=>c.id===mode.componentID);
  packages.value=current?current.package:component?.package||mode.group||(mode.kind==='package'?mode.id:report.module);
  el('label','Exact top-level unit-test names (comma or space separated)',form);
  const run=el('input',undefined,form);run.required=true;run.placeholder='TestMyScenario';run.setAttribute('aria-label','Unit-test names');
  const saved=(report.scope?.unitSelections||[]).find(s=>s.package===packages.value);
  if(saved)run.value=saved.tests.join(', ');
  const consent=el('label',undefined,form);
  const check=el('input',undefined,consent);check.type='checkbox';check.required=true;
  check.setAttribute('aria-label','Authorize selected unit tests');
  consent.append(document.createTextNode(' I authorize execution of these selected tests.'));
  const runtimeLabel=el('label',undefined,form);
  const runtime=el('input',undefined,runtimeLabel);runtime.type='checkbox';
  runtime.setAttribute('aria-label','Include standard Go runtime trace');
  runtimeLabel.append(document.createTextNode(' Include optional standard Go runtime trace (concurrency evidence).'));
  el('button','Collect unit-test coverage',form);
  form.onsubmit=async e=>{
    e.preventDefault();
    if(!check.checked)return;
    await collect('/api/unit-tests',{execute:true,saveSelections:true,runtimeTrace:runtime.checked,selections:[{package:packages.value,tests:run.value.split(/[\s,]+/).filter(Boolean)}]},'Running explicitly selected unit tests and collecting architectural coverage…');
  };
};
