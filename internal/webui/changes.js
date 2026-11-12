'use strict';
const changeState={active:false,id:null,overview:null,perspective:'flows',query:'',status:'all',offset:0,
  side:'after',flow:null,record:null,fileID:null,inventoryFile:null,expanded:[],focusPath:[],focusSymbolID:undefined,history:[],serial:0};
const changeEl=id=>document.getElementById(id);
function changeNotice(text,error=false) {
  changeEl('change-notice').textContent=text;changeEl('change-notice').classList.toggle('error',error);
}
async function changeRequest(path,body) {
  const response=await fetch(path,body===undefined?undefined:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const result=await response.json();
  if(!response.ok)throw Error(result.error||response.statusText);
  return result;
}
function changeQuery(operation,args={}) {
  return changeRequest('/api/changes/query',{comparisonID:changeState.id,operation,...args});
}
async function changeNavigate(input,expectedFingerprint,signal) {
  const check=()=>{
    signal?.throwIfAborted();
    if(expectedFingerprint!==undefined&&chatFingerprint()!==expectedFingerprint)throw Error('View changed manually; automatic comparison navigation canceled.');
  };
  check();
  const response=await fetch('/api/changes/query',{method:'POST',signal,headers:{'Content-Type':'application/json'},body:JSON.stringify(input)});
  const evidence=await response.json();if(!response.ok)throw Error(evidence.error||'Comparison navigation unavailable');
  check();
  if(typeof qualityState!=='undefined'&&qualityState.active)qualityClose();
  if(input.comparisonID!==changeState.id){
    const status=await changeRequest('/api/changes');check();
    if(status.comparisonID!==input.comparisonID)throw Error('Comparison changed; stale navigation rejected');
    changeAdoptComparison(status.comparisonID);
  }
  if(input.comparisonID!==changeState.id)throw Error('Comparison changed; stale navigation rejected');
  if(!changeState.active){
    changeState.active=true;changeEl('change-workspace').hidden=false;document.body.classList.add('change-review-open');
    changeEl('change-review').setAttribute('aria-pressed','true');document.body.append(changeEl('chat-panel'));
    workspaceNavigation();
  }
  // The authorized transition itself changes context; subsequent checks guard
  // against manual movement from this new context rather than the old view.
  expectedFingerprint=expectedFingerprint===undefined?undefined:chatFingerprint();
  if(!changeState.overview){await changeRefresh();expectedFingerprint=expectedFingerprint===undefined?undefined:chatFingerprint();}
  if(input.operation==='record')await changeSelectRecord(input.recordID);
  else if(input.operation==='source')await changeCompare(input.fileID);
  else if(input.operation==='flow'){
    const found=await changeQuery('flows',{query:undefined});
    let flow=found.items.find(f=>f.id===input.flowID),cursor=found.nextOffset;
    while(!flow&&cursor!==null){const next=await changeQuery('flows',{offset:cursor});flow=next.items.find(f=>f.id===input.flowID);cursor=next.nextOffset;check();}
    if(!flow)throw Error('Selected flow is no longer available');check();
    changeState.side=input.side;await changeSelectFlow(flow);
    if(input.focusSymbolID||input.expandedIDs?.length){
      changeState.focusSymbolID=input.focusSymbolID;changeState.focusPath=input.focusPath||[];changeState.expanded=input.expandedIDs||[];
      await changeRenderFlow();
    }
  }else if(input.operation==='side'){
    const sideInput={...input};
    if(sideInput.functionView==='logical')sideInput.view='logical';
    if(sideInput.functionView==='control')sideInput.section='controlFlowNodes';
    if(sideInput.functionView==='calls')sideInput.section='calls';
    await changeShowSide(sideInput,sideInput.functionView||sideInput.view);
  }else {
    changeState.status=input.status||'all';changeState.query=input.query||'';
    changeEl('change-search').value=changeState.query;changeEl('change-filter').value=changeState.status;
    await changePerspective(['flows','files'].includes(input.operation)?input.operation:input.perspective||'all');
  }
  chatState.lastNavigation=chatContext();
}
async function changeApplyProposal(proposal) {
  if(proposal.action==='state'){
    if(proposal.comparisonID!==changeState.id)throw Error('Stale progress proposal; reload current comparison');
    if(!confirm(`Update these exact local review identifiers?\n${JSON.stringify(proposal,null,2)}`))return;
    const {action,directory,before,after,remote,mergeBase,...state}=proposal;
    await changeRequest('/api/changes/state',{...state,confirmed:true});await changeRefresh();
  }else if(proposal.action==='resolve'){
    if(!confirm('Resolve Git revisions and fetch the source remote if supplied? This does not collect analysis or change the checkout.'))return;
    await changeOpen();changeEl('change-inputs').open=true;
    changeEl('change-directory').value=proposal.directory;changeEl('change-before').value=proposal.before||'';
    changeEl('change-after').value=proposal.after||'';changeEl('change-remote').value=proposal.remote||'';
    changeEl('change-merge-base').checked=!!proposal.mergeBase;
    changeEl('change-input-form').requestSubmit();
  }else if(proposal.action==='collect'){
    if(changeState.preview?.previewID!==proposal.previewID||changeState.preview.before.commit!==proposal.before||changeState.preview.after.commit!==proposal.after)throw Error('Stale collection proposal; resolve and inspect the pinned revision preview again');
    await changeOpen();changeEl('change-inputs').open=true;changeEl('change-confirm').click();
  }else if(proposal.action==='cancel'){
    if(changeState.jobID!==proposal.jobID)throw Error('Stale cancellation proposal; reload collection progress');
    changeEl('change-cancel').click();
  }
  else throw Error('Unsupported local action proposal');
}
function changeSaveNavigation() {
  changeState.history.push({perspective:changeState.perspective,offset:changeState.offset,side:changeState.side,
    flow:changeState.flow,record:changeState.record,fileID:changeState.fileID,inventoryFile:changeState.inventoryFile,expanded:[...changeState.expanded],
    focusPath:[...changeState.focusPath],focusSymbolID:changeState.focusSymbolID,viewport:changeState.viewport?{...changeState.viewport}:null,
    detailInput:changeState.detailInput?{...changeState.detailInput}:null,detailLabel:changeState.detailLabel});
}
function changeClose() {
  changeState.active=false;changeState.serial++;
  changeEl('change-workspace').hidden=true;document.body.classList.remove('change-review-open','change-wide');
  changeEl('change-review').setAttribute('aria-pressed','false');
  workspaceNavigation();
}
function changeAdoptComparison(id) {
  if(id===changeState.id)return;
  Object.assign(changeState,{id,overview:null,perspective:'flows',query:'',status:'all',offset:0,side:'after',
    flow:null,record:null,fileID:null,inventoryFile:null,expanded:[],focusPath:[],focusSymbolID:undefined,history:[],
    detailInput:null,detailLabel:null,viewport:null});
  changeState.serial++;
  changeEl('change-search').value='';changeEl('change-filter').value='all';
  for(const name of ['change-list','change-detail','change-flow','change-flow-tools','change-reader'])changeEl(name).replaceChildren();
  changeEl('change-reader').hidden=true;document.body.classList.remove('change-wide');
}
function changeRenderPreview(preview) {
  const target=changeEl('change-preview');target.replaceChildren();
  changeEl('change-confirm').hidden=!preview;
  if(!preview)return;
  el('pre',`Before: ${preview.before.label}\n${preview.before.commit}\nAfter: ${preview.after.label}\n${preview.after.commit}`+
    (preview.target?`\nTarget tip (not Before): ${preview.target.commit}`:''),target);
  el('p','Collect static source evidence and prepare Go exports. No target tests/scripts execute. Replacing a comparison binds progress to the new evidence identity.',target);
}
async function changeOpen() {
  changeState.active=true;changeEl('change-workspace').hidden=false;
  document.body.append(changeEl('chat-panel'));
  document.body.classList.add('change-review-open');changeEl('change-review').setAttribute('aria-pressed','true');
  workspaceNavigation();
  changeEl('change-directory').value=changeEl('directory').value||report?.scope?.root||report?.root||'';
  try {
    const status=await changeRequest('/api/changes');
    changeAdoptComparison(status.comparisonID||null);
    changeState.preview=status.preview||null;changeState.jobID=status.jobID;
    changeRenderPreview(changeState.preview);
    changeEl('change-cancel').hidden=!status.running;
    if(changeState.id)await changeRefresh();
    else {
      changeEl('change-inputs').open=true;
      changeNotice('Compare committed revisions to discover affected flows and complete supporting changes.');
    }
    if(status.running)changePoll();
    else if(status.error)changeNotice(status.error,true);
  }catch(error){changeNotice(error.message,true);}
}
async function changeRefresh() {
  const comparisonID=changeState.id;
  const overview=await changeQuery('overview');
  if(!changeState.active||changeState.id!==comparisonID)return;
  changeState.overview=overview;
  let moduleCursor=overview.modules.nextOffset;
  while(moduleCursor!==null){const more=await changeQuery('overview',{offset:moduleCursor});overview.modules.items.push(...more.modules.items);moduleCursor=more.modules.nextOffset;}
  if(!changeState.active||changeState.id!==comparisonID)return;
  const r=overview.revisions;
  changeEl('change-heading').textContent=`Change Review · ${r.before.label} (${r.before.commit.slice(0,8)}) → ${r.after.label} (${r.after.commit.slice(0,8)})`;
  changeEl('change-heading').title=`Before ${r.before.commit}\nAfter ${r.after.commit}`+(r.target?`\nTarget tip ${r.target.commit}`:'');
  const p=overview.progress;
  if(changeState.perspective==='flows'&&p.flows===0&&overview.fileCount>0){
    changeState.perspective='files';
    changeNotice('No source-supported flows are available. Showing the complete Git inventory; semantic limitations remain inspectable.');
  }
  changeEl('change-progress').textContent=`${p.reviewed} of ${p.total} unique changes reviewed; ${p.considered} of ${p.flows} flow contexts considered`+
    (p.followUp?`; ${p.followUp} need follow-up`:'')+'. Local tracking—not approval or correctness certification.';
  const provenance=changeEl('change-provenance-body');provenance.replaceChildren();
  changeEl('change-provenance').querySelector('summary').textContent=`Scope, provenance and evidence limitations (${overview.limitations.total??overview.limitations.items.length})`;
  el('pre',JSON.stringify({revisions:r,scope:overview.scope,environment:overview.environment},null,2),provenance);
  for(const limitation of overview.limitations.items)el('p',limitation,provenance);
  changeMore(provenance,overview.limitations,async offset=>(await changeQuery('overview',{offset})).limitations,
    items=>{for(const limitation of items)el('p',limitation,provenance);},'More evidence limitations');
  for(const module of overview.modules.items){
    el('h4',module.path,provenance);
    for(const side of ['before','after']){
      const evidence=module[side];
      el('p',`${side}: ${evidence.absent?'absent':evidence.error?'analysis unavailable: '+evidence.error:evidence.partial?'partial analysis; inspect type errors':'static analysis retained'}`,provenance);
    }
  }
  await changeList();
}
async function changeList() {
  const serial=++changeState.serial;
  const files=changeState.perspective==='files',flows=changeState.perspective==='flows';
  const operation=files?'files':flows?'flows':'records';
  const result=await changeQuery(operation,{offset:changeState.offset,query:changeState.query,status:changeState.status,
    perspective:!files&&!flows?changeState.perspective:undefined});
  if(!changeState.active||serial!==changeState.serial)return;
  const list=changeEl('change-list');list.replaceChildren();
  el('p',`${result.total} ${flows?'affected flows':files?'changed files':'change records'}${result.filteredOut?` · ${result.filteredOut} filtered out`:''}`,list);
  if(!result.total) {
    el('p',files?(changeState.overview?.fileCount===0?'No Git changes between these commits.':'No changed files match this view. Clear search or local-state filters to inspect the complete inventory.'):flows?'No flows associated in this view. The complete inventory still accounts for supporting or unmapped changes.':'No changes match this view.',list);
    if(flows)button('Inspect all changes',()=>changePerspective('all'),list);
  }
  for(const item of result.items) {
    const title=flows?item.name:files?`${item.status}: ${item.after.path||item.before.path}`:item.title;
    button(title,()=>changeAct(()=>flows?changeSelectFlow(item):files?changeSelectFile(item):changeSelectRecord(item.id)),list);
    if(!flows&&!files)el('small',`${item.reviewed} / ${item.itemCount} reviewed · ${item.flowCount} flow contexts`,list);
    if(flows)el('small',`${item.status} · ${item.recordCount} changed responsibilities · ${item.considered?'context considered':'context not considered'}`,list);
  }
  if(changeState.offset)button('Previous',()=>{changeState.offset=Math.max(0,changeState.offset-10);changeAct(changeList);},list);
  if(result.nextOffset!==null)button('Next',()=>{changeState.offset=result.nextOffset;changeAct(changeList);},list);
}
async function changePerspective(value) {
  changeSaveNavigation();changeState.perspective=value;changeState.offset=0;
  changeState.flow=null;changeState.record=null;changeState.fileID=null;changeState.inventoryFile=null;
  changeState.detailInput=null;
  changeEl('change-reader').hidden=true;changeEl('change-detail').replaceChildren();
  changeEl('change-flow').replaceChildren();changeEl('change-flow-tools').replaceChildren();
  document.body.classList.remove('change-wide');
  await changeAct(changeList);
}
async function changeAct(action) {try{await action();}catch(error){changeNotice(error.message,true);}}
function changeSideSnapshot(flow=changeState.flow) {
  const module=changeState.overview.modules.items.find(m=>m.id===flow?.moduleID);
  if(!module)throw Error('Selected module is not loaded; inspect module discovery before opening its evidence.');
  return {moduleID:module.id,side:changeState.side,snapshotID:module[changeState.side].snapshotID};
}
async function changeSelectFlow(flow,remember=true) {
  if(remember)changeSaveNavigation();
  changeState.flow=flow;changeState.record=null;changeState.fileID=null;changeState.inventoryFile=null;changeState.expanded=[];changeState.focusPath=[];changeState.focusSymbolID=undefined;changeState.viewport=null;
  if(!flow[changeState.side])changeState.side=flow.before?'before':'after';
  document.body.classList.remove('change-wide');changeEl('change-reader').hidden=true;
  await changeRenderFlow();
}
function changeMore(parent,page,load,append,label='More') {
  let cursor=page.nextOffset;if(cursor===null)return;
  const control=button(label,()=>changeAct(async()=>{
    control.disabled=true;
    try {const next=await load(cursor);append(next.items);cursor=next.nextOffset;if(cursor===null)control.remove();}
    finally{control.disabled=false;}
  }),parent);
}
async function changeFlowRecords(flow) {
  const records=await changeQuery('records',{flowID:flow.id,status:changeState.status});
  if(!changeState.active||changeState.flow?.id!==flow.id)return;
  const detail=changeEl('change-detail');detail.replaceChildren();
  el('h3',flow.name,detail);el('p',`${flow.status} · ${records.total} changed responsibilities. Hidden/shared changes remain listed even when diagram topology is unchanged.`,detail);
  button(flow.considered?'Mark context not considered':'Mark flow context considered',()=>changeAct(()=>changeMark({flowID:flow.id,considered:!flow.considered})),detail);
  const module=changeState.overview.modules.items.find(m=>m.id===flow.moduleID);
  const append=items=>{for(const record of items){
    const missingAfter=!record.after&&record.before;
    const prefix=missingAfter?(module?.after?.error||module?.after?.partial?'After unavailable: ':'Removed: '):'';
    button(`${prefix}${record.title} · ${record.itemCount} changes`,()=>changeAct(()=>changeSelectRecord(record.id)),detail);
  }};
  append(records.items);
  changeMore(detail,records,offset=>changeQuery('records',{flowID:flow.id,status:changeState.status,offset}),
    append,'More changes in this flow');
}
async function changeReadScene(args) {
  const first=await changeQuery('flow',args),scene=first.evidence;
  for(const [section,key,paging] of [['nodes','nodes','nodePage'],['edges','edges','edgePage']]) {
    let offset=scene[paging]?.nextOffset;
    while(offset!==undefined&&offset!==null){
      const next=await changeQuery('flow',{...args,section,offset});
      scene[key].push(...next.evidence.items);offset=next.evidence.nextOffset;
    }
  }
  return scene;
}
async function changeRenderFlow() {
  const flow=changeState.flow;if(!flow)return;
  const serial=++changeState.serial;
  const args={...changeSideSnapshot(flow),flowID:flow.id,expandedIDs:changeState.expanded,
    focusSymbolID:changeState.focusSymbolID,focusPath:changeState.focusPath};
  const scene=await changeReadScene(args);
  if(!changeState.active||serial!==changeState.serial)return;
  const tools=changeEl('change-flow-tools');tools.replaceChildren();
  el('strong',`${changeState.side==='before'?'Before':'After'} · ${flow.name}`,tools);
  for(const side of ['before','after']) {
    const control=button(side==='before'?'Before':'After',()=>changeAct(async()=>{
      if(!flow[side])throw Error(`No matched ${side} flow; retain the available owning context.`);
      changeSaveNavigation();
      const path=[...changeState.focusPath,...(changeState.focusSymbolID?[changeState.focusSymbolID]:[])],matched=[];
      for(const symbolID of path){
        const values=await changeQuery('correspondences',{moduleID:flow.moduleID,symbolID});
        if(values.items.length!==1){changeNotice('No unique counterpart for this focus; showing its nearest matched parent.');break;}
        matched.push(values.items[0][side==='before'?'beforeID':'afterID']);
      }
      changeState.side=side;changeState.focusSymbolID=matched.pop();changeState.focusPath=matched;changeState.expanded=[];changeState.viewport=null;
      await changeRenderFlow();
    }),tools);
    control.disabled=!flow[side];control.setAttribute('aria-pressed',String(changeState.side===side));
  }
  const fullContext=button('Show full flow context',()=>changeAct(async()=>{
    changeSaveNavigation();changeState.focusSymbolID=undefined;changeState.focusPath=[];changeState.expanded=[];changeState.viewport=null;
    await changeRenderFlow();
  }),tools);
  fullContext.disabled=!changeState.focusSymbolID&&!changeState.focusPath.length;
  fullContext.title=fullContext.disabled?'The full flow already includes unchanged source context.':'Return to the owning entry and its unchanged source context.';
  changeDraw(scene);
  await changeFlowRecords(flow);
}
function changeDraw(scene,inspectNode) {
  const host=changeEl('change-flow');host.replaceChildren();
  const ns='http://www.w3.org/2000/svg',svgNode=document.createElementNS(ns,'svg');
  svgNode.setAttribute('aria-label','Pinned revision source flow; possible paths, not runtime chronology');svgNode.setAttribute('role','group');
  host.append(svgNode);
  const add=(tag,attrs,parent=svgNode)=>{const node=document.createElementNS(ns,tag);for(const [key,value]of Object.entries(attrs))node.setAttribute(key,value);parent.append(node);return node;};
  const defs=add('defs',{}),marker=add('marker',{id:'change-arrow',viewBox:'0 0 10 10',refX:9,refY:5,markerWidth:7,markerHeight:7,orient:'auto-start-reverse'},defs);
  add('path',{d:'M0,0 L10,5 L0,10 z',fill:'#697d90'},marker);
  const layout=GoReviewVisual.layout(scene,scene.nodes.map(n=>n.id));
  const coords=new Map(layout.placements.map(p=>[p.id,{x:(p.column-1)*320+20,y:(p.row-1)*115+20}]));
  for(const [i,id]of layout.background.entries())coords.set(id,{x:680,y:i*115+20});
  const boxes=new Map(),edges=[];
  let maxY=150,maxX=340;
  const links=add('g',{});
  for(const edge of scene.edges){if(!coords.has(edge.from)||!coords.has(edge.to))continue;edges.push({edge,line:add('path',{fill:'none',stroke:'#697d90','stroke-width':1.5,'marker-end':'url(#change-arrow)',
    'stroke-dasharray':['reference','asynchronous'].includes(edge.kind)?'5 4':'none'},links)});}
  const position=()=>{for(const {edge,line}of edges){const a=coords.get(edge.from),b=coords.get(edge.to);line.setAttribute('d',`M${a.x+130},${a.y+70} L${b.x+130},${b.y}`);}for(const [id,g]of boxes){const p=coords.get(id);g.setAttribute('transform',`translate(${p.x},${p.y})`);}};
  for(const node of scene.nodes) {
    const p=coords.get(node.id);if(!p)continue;
    maxY=Math.max(maxY,p.y+120);maxX=Math.max(maxX,p.x+300);
    const g=add('g',{class:'change-node'+(node.change?' changed':''),tabindex:0,role:'button','aria-label':node.title+(node.change?' · '+node.change.label:'')});
    boxes.set(node.id,g);
    add('title',{},g).textContent=node.title||node.label||node.id;
    add('rect',{width:260,height:72,rx:7},g);
    const title=add('text',{x:10,y:22,'font-size':12},g);title.textContent=(node.title||node.label||node.id).slice(0,35);
    const subtitle=add('text',{x:10,y:42,'font-size':10},g);subtitle.textContent=node.childCount?`${node.childCount} child stages · Expand / Focus`:node.kind||'Source stage';
    const annotation=add('text',{x:10,y:60,'font-size':10},g);annotation.textContent=[node.change?.label,
      Number.isFinite(node.metrics?.complexity?.value)?`Max CC ${node.metrics.complexity.value}`:null].filter(Boolean).join(' · ');
    const inspect=()=>changeAct(async()=>{
      if(inspectNode){await inspectNode(node);return;}
      const detail=changeEl('change-detail');detail.replaceChildren();el('h3',node.title,detail);
      for(const recordID of node.change?.recordIDs||[])button('Inspect '+node.change.label,()=>changeAct(()=>changeSelectRecord(recordID)),detail);
      if(node.symbolID) {
        const facts=await changeQuery('side',{...changeSideSnapshot(),view:'evidence',symbolID:node.symbolID});
        el('pre',JSON.stringify(facts.evidence,null,2),detail);
      }
      if(node.childCount)button(changeState.expanded.includes(node.id)?'Collapse in place':'Expand in place',()=>changeAct(async()=>{
        changeSaveNavigation();changeState.expanded=changeState.expanded.includes(node.id)?changeState.expanded.filter(id=>id!==node.id):[...changeState.expanded,node.id];await changeRenderFlow();
      }),detail);
      if(node.symbolID)button('Focus this responsibility',()=>changeAct(async()=>{
        changeSaveNavigation();if(changeState.focusSymbolID)changeState.focusPath.push(changeState.focusSymbolID);
        changeState.focusSymbolID=node.symbolID;changeState.expanded=[];await changeRenderFlow();
      }),detail);
      if(changeState.flow)button('Changes in enclosing flow',()=>changeAct(()=>changeFlowRecords(changeState.flow)),detail);
    });
    g.onclick=inspect;g.onkeydown=event=>{
      if(event.key==='Enter'||event.key===' '){event.preventDefault();inspect();return;}
      const direction={ArrowLeft:[-1,0],ArrowRight:[1,0],ArrowUp:[0,-1],ArrowDown:[0,1]}[event.key];
      if(event.altKey&&direction){
        event.preventDefault();event.stopPropagation();
        const p=coords.get(node.id),step=event.shiftKey?36:12;
        coords.set(node.id,{x:p.x+direction[0]*step,y:p.y+direction[1]*step});position();
      }
    };
    let drag;
    g.onpointerdown=event=>{
      event.stopPropagation();drag={x:event.clientX,y:event.clientY,origin:{...coords.get(node.id)}};
      g.setPointerCapture?.(event.pointerId);
    };
    g.onpointermove=event=>{
      if(!drag)return;
      const rect=svgNode.getBoundingClientRect(),box=changeState.viewport;
      coords.set(node.id,{x:drag.origin.x+(event.clientX-drag.x)*box.w/Math.max(rect.width,1),y:drag.origin.y+(event.clientY-drag.y)*box.h/Math.max(rect.height,1)});position();
    };
    g.onpointerup=g.onpointercancel=()=>{drag=null;};
  }
  position();
  const initial={x:0,y:0,w:maxX,h:maxY};
  changeState.viewport=changeState.viewport||initial;
  const apply=()=>{const v=changeState.viewport;svgNode.setAttribute('viewBox',`${v.x} ${v.y} ${v.w} ${v.h}`);};
  apply();
  svgNode.onwheel=event=>{
    event.preventDefault();
    const v=changeState.viewport;
    if(event.ctrlKey||event.metaKey||event.shiftKey){
      const factor=event.deltaY>0?1.12:.9;
      changeState.viewport={...v,w:v.w*factor,h:v.h*factor};
    }else{
      const rect=svgNode.getBoundingClientRect();
      changeState.viewport={...v,x:v.x+event.deltaX*v.w/Math.max(rect.width,1),y:v.y+event.deltaY*v.h/Math.max(rect.height,1)};
    }
    apply();
  };
  let pan;
  svgNode.onpointerdown=event=>{pan={x:event.clientX,y:event.clientY,view:{...changeState.viewport}};svgNode.setPointerCapture?.(event.pointerId);};
  svgNode.onpointermove=event=>{if(!pan)return;const rect=svgNode.getBoundingClientRect();changeState.viewport={...pan.view,x:pan.view.x-(event.clientX-pan.x)*pan.view.w/Math.max(rect.width,1),y:pan.view.y-(event.clientY-pan.y)*pan.view.h/Math.max(rect.height,1)};apply();};
  svgNode.onpointerup=svgNode.onpointercancel=()=>{pan=null;};
  button('Fit flow',()=>{changeState.viewport=initial;apply();},changeEl('change-flow-tools'));
  for(const [label,factor]of [['Zoom in',.8],['Zoom out',1.25]])button(label,()=>{
    changeState.viewport={...changeState.viewport,w:changeState.viewport.w*factor,h:changeState.viewport.h*factor};apply();
  },changeEl('change-flow-tools'));
  host.onkeydown=event=>{
    const direction={ArrowLeft:[-1,0],ArrowRight:[1,0],ArrowUp:[0,-1],ArrowDown:[0,1]}[event.key];
    if(!direction)return;event.preventDefault();
    changeState.viewport={...changeState.viewport,x:changeState.viewport.x+direction[0]*changeState.viewport.w/10,y:changeState.viewport.y+direction[1]*changeState.viewport.h/10};apply();
  };
}
async function changeSelectRecord(id,remember=true) {
  if(remember)changeSaveNavigation();
  const serial=++changeState.serial;
  const record=await changeQuery('record',{recordID:id});
  if(!changeState.active||serial!==changeState.serial)return;
  changeState.record=record;
  changeState.inventoryFile=null;
  changeState.detailInput=null;
  changeState.fileID=null;changeEl('change-reader').hidden=true;document.body.classList.remove('change-wide');
  const detail=changeEl('change-detail');detail.replaceChildren();
  el('h3',record.title,detail);el('p',record.reason,detail);
  el('p',`${record.itemCount} unique changed items · ${record.flowCount} associated flow contexts`,detail);
  const ids=record.items.items;
  const disclosure=el('details',undefined,detail);el('summary',`Action applies to these ${ids.length} displayed item IDs`,disclosure);
  const itemList=el('pre',ids.join('\n'),disclosure),actions=[];
  for(const [status,label]of [['reviewed','Mark reviewed'],['follow-up','Needs follow-up'],['not-reviewed','Undo review mark']])
    actions.push({control:button(`${label} (${ids.length} items)`,()=>changeAct(()=>changeMark({itemIDs:ids,status})),detail),label});
  changeMore(detail,record.items,async offset=>(await changeQuery('record',{recordID:id,offset})).items,
    items=>{ids.push(...items);itemList.textContent=ids.join('\n');for(const action of actions)action.control.textContent=`${action.label} (${ids.length} items)`;},'Include next item IDs in this explicit action set');
  const appendFiles=items=>{for(const fileID of items)button('Compare retained file evidence',()=>changeAct(()=>changeCompare(fileID,record)),detail);};
  appendFiles(record.files.items);
  changeMore(detail,record.files,async offset=>(await changeQuery('record',{recordID:id,offset})).files,appendFiles,'More retained files');
  if(record.moduleID)for(const side of ['before','after']){
    const owner=record[side];if(!owner)continue;
    for(const [view,label,section]of [['logical','Logical flow',undefined],['evidence','Control flow','controlFlowNodes'],['evidence','Call relationships','calls'],['dataflow','Value data flow',undefined]])
      button(`${side}: ${label}`,()=>changeAct(async()=>{
        const module=changeState.overview.modules.items.find(m=>m.id===record.moduleID);
        await changeShowSide({moduleID:record.moduleID,side,snapshotID:module[side].snapshotID,view,symbolID:owner.symbolID,section},label);
      }),detail);
  }
  const appendFlows=items=>{for(const flowID of items)button('Show inclusion witness',()=>changeAct(async()=>{
    const witness=await changeQuery('witnesses',{flowID,recordID:id});el('pre',JSON.stringify(witness.items,null,2),detail);
    for(const association of witness.items)button(`Read all ${association.witnessCount} ${association.side} inclusion steps`,()=>changeAct(async()=>{
      const steps=await changeQuery('witnesses',{flowID,recordID:id,section:'steps',associationIndex:association.associationIndex});
      el('pre',JSON.stringify(steps.items,null,2),detail);
      changeMore(detail,steps,offset=>changeQuery('witnesses',{flowID,recordID:id,section:'steps',associationIndex:association.associationIndex,offset}),
        items=>el('pre',JSON.stringify(items,null,2),detail),'More source steps');
    }),detail);
  }),detail);};
  appendFlows(record.flows.items);
  changeMore(detail,record.flows,async offset=>(await changeQuery('record',{recordID:id,offset})).flows,appendFlows,'More associated flows');
}
async function changeSideItems(input,section) {
  const items=[];let offset=0;
  while(offset!==null){
    const result=await changeQuery('side',{...input,section,offset});
    if(!Array.isArray(result.evidence.items))throw Error(result.evidence.reason||result.evidence.status||'Requested detail was not retained');
    items.push(...result.evidence.items);offset=result.evidence.nextOffset;
  }
  return items;
}
async function changeShowSide(input,label,remember=true) {
  if(remember)changeSaveNavigation();
  changeState.detailInput=input;changeState.detailLabel=label;changeState.fileID=null;changeState.side=input.side;
  changeState.viewport=null;
  const serial=++changeState.serial;
  const readInput={...input};
  if(input.view==='source'&&input.endLine!==undefined)readInput.endLine=Math.min(input.endLine,(input.startLine||1)+499);
  const value=await changeQuery('side',readInput),facts=value.evidence;
  if(!changeState.active||serial!==changeState.serial)return;
  const tools=changeEl('change-flow-tools');tools.replaceChildren();el('strong',`${input.side}: ${label}`,tools);
  const reader=changeEl('change-reader');reader.hidden=false;reader.replaceChildren();
  el('h3',`${input.side}: ${label}`,reader);
  if(input.view==='source'||input.view==='document'){
    const append=(page,args)=>{
      el('p',`${page.path||page.file?.path||''}${page.startLine?':'+page.startLine:''}`,reader);
      el('pre',page.text??JSON.stringify(page,null,2),reader);
      let next;
      if(page.textPage?.nextOffset!==null&&page.textPage?.nextOffset!==undefined)
        next={...args,textOffset:page.textPage.nextOffset};
      else if(input.view==='source'&&page.endLine<Math.min(input.endLine??page.totalLines,page.totalLines))
        next={...input,startLine:page.endLine+1,endLine:Math.min(input.endLine??page.totalLines,page.endLine+500,page.totalLines),textOffset:0};
      if(next){
        const more=button('Continue retained text',()=>changeAct(async()=>{
          const result=await changeQuery('side',next);
          if(!changeState.active||serial!==changeState.serial)return;
          more.remove();append(result.evidence,next);
        }),reader);
      }
    };
    append(facts,readInput);return;
  }
  const node=(id,title,extra={})=>({id,title,childCount:0,kind:'source',...extra});
  let nodes=[],edges=[],root=input.symbolID;
  if(input.view==='logical'){
    nodes=[node(root,facts.symbol.name||facts.symbol.qualified,{symbolID:root})];
    for(const key of ['crossPackage','samePackage','references','callers']){
      const groups=await changeSideItems(input,key);
      for(const group of groups){
        const id=key+':'+group.symbol.id;
        nodes.push(node(id,group.symbol.name||group.symbol.qualified,{symbolID:group.symbol.id,location:group.symbol.location,relationship:key}));
        edges.push({from:key==='callers'?id:root,to:key==='callers'?root:id,kind:key==='references'?'reference':'call'});
      }
    }
    for(const [i,boundary]of (await changeSideItems(input,'boundaries')).entries()){
      const id='boundary:'+i;nodes.push(node(id,boundary.target,{location:boundary.location,kind:boundary.kind}));edges.push({from:root,to:id,kind:'call'});
    }
    el('p',facts.limitations.join(' '),reader);
  }else if(input.view==='dataflow'){
    const values=await changeSideItems(input,'nodes'),links=await changeSideItems(input,'edges');
    nodes=values.map(n=>node(n.id,n.label||n.name||n.kind,{...n,title:n.label||n.name||n.kind}));
    edges=links;root=nodes[0]?.id;
    el('p','Retained value definitions and operand uses, not runtime values or alias lineage.',reader);
  }else if(input.section==='controlFlowNodes'){
    const values=await changeSideItems(input,'controlFlowNodes'),links=await changeSideItems(input,'controlFlowEdges');
    nodes=values.map(n=>node(n.id,n.label||n.text||n.kind,{location:n.location,kind:n.kind}));
    edges=links;root=nodes[0]?.id;
    el('p','Possible statement paths in this revision. Missing coverage remains unknown.',reader);
  }else if(input.section==='calls'){
    const calls=await changeSideItems(input,'calls');
    nodes=[node(root,changeState.record?.[input.side]?.name||'Selected declaration',{symbolID:root})];
    for(const call of calls){
      const outgoing=call.from===root,id=call.id;
      nodes.push(node(id,call.target||call.to||call.from,{symbolID:outgoing?call.to:call.from,location:call.location,kind:call.kind}));
      edges.push({from:outgoing?root:id,to:outgoing?id:root,kind:call.execution==='async'?'asynchronous':'call'});
    }
    el('p','Distinct source call sites are separate occurrences. External or unresolved targets remain boundaries.',reader);
  }else {el('pre',JSON.stringify(facts,null,2),reader);return;}
  if(!nodes.length){el('p','No graph was retained for this scope; inspect its recorded limitations.',reader);return;}
  changeDraw({entry:root,nodes,edges},async selected=>{
    const detail=changeEl('change-detail');
    detail.replaceChildren();el('h3',selected.title,detail);
    if(selected.symbolID) {
      button('Follow pinned logical flow',()=>changeAct(()=>changeShowSide({...input,view:'logical',section:undefined,symbolID:selected.symbolID},'Logical flow')),detail);
      const evidence=await changeQuery('side',{...input,view:'evidence',section:undefined,symbolID:selected.symbolID});
      el('pre',JSON.stringify(evidence.evidence.symbol,null,2),detail);
    }
    if(selected.location)button('Read pinned source',()=>changeAct(()=>changeShowSide({...input,view:'source',section:undefined,
      sourceFileID:selected.location.fileID,startLine:selected.location.line,endLine:selected.location.endLine},'Retained source')),detail);
    if(input.view==='logical'&&selected.relationship)button('Read relationship source witnesses',()=>changeAct(async()=>{
      const sites=await changeSideItems({...input,nodeID:selected.symbolID},'sites');el('pre',JSON.stringify(sites,null,2),detail);
    }),detail);
  });
}
async function changeSelectFile(file,remember=true) {
  if(remember)changeSaveNavigation();
  const serial=++changeState.serial;
  changeState.record=null;changeState.flow=null;changeState.detailInput=null;changeState.inventoryFile=file;changeState.fileID=file.id;
  changeEl('change-reader').hidden=true;document.body.classList.remove('change-wide');
  const detail=changeEl('change-detail');detail.replaceChildren();
  el('h3',file.after.path||file.before.path,detail);
  el('p',`${file.status} · ${file.content} · ${file.itemCount} unique items`,detail);
  el('p',`Before: ${file.beforeEligibility||'Eligibility not recorded'}. After: ${file.afterEligibility||'Eligibility not recorded'}.`,detail);
  button('Read retained before/after',()=>changeAct(()=>changeCompare(file.id)),detail);
  const items=await changeQuery('items',{fileID:file.id});
  if(!changeState.active||serial!==changeState.serial)return;
  el('pre',JSON.stringify(items.items,null,2),detail);
  const ids=items.items.map(i=>i.id);
  changeMore(detail,items,offset=>changeQuery('items',{fileID:file.id,offset}),more=>{
    ids.push(...more.map(i=>i.id));el('pre',JSON.stringify(more,null,2),detail);
  },'More items in this file');
  for(const [status,label]of [['reviewed','Mark displayed items reviewed'],['follow-up','Displayed items need follow-up'],['not-reviewed','Undo displayed review marks']])
    button(label,()=>changeAct(()=>changeMark({itemIDs:ids,status})),detail);
}
async function changeCompare(fileID,record,remember=true) {
  if(remember)changeSaveNavigation();
  const serial=++changeState.serial,comparisonID=changeState.id;
  const current=()=>changeState.active&&serial===changeState.serial&&comparisonID===changeState.id;
  changeState.detailInput=null;changeState.inventoryFile=null;
  changeState.fileID=fileID;
  const reader=changeEl('change-reader');reader.hidden=false;reader.replaceChildren();document.body.classList.add('change-wide');
  el('h3',record?.title||'Retained file comparison',reader);
  button('Close comparison reader',()=>{changeState.serial++;reader.hidden=true;document.body.classList.remove('change-wide');},reader);
  const status=el('p','Loading retained source for both revisions…',reader);
  const sources={};
  for(const side of ['before','after']){
    try {
      let offset=0,text='',first;
      do {
        const page=await changeQuery('source',{fileID,side,textOffset:offset,textLimit:6000});
        if(!current())return;
        if(!first)first=page;
        if(page.textPage.total>250000)throw Error('Retained source exceeds the 250,000 character diff limit.');
        text+=page.text;
        if(page.textPage.nextOffset!==null&&page.textPage.nextOffset<=offset)throw Error('Retained source pagination did not advance.');
        offset=page.textPage.nextOffset;
      } while(offset!==null);
      sources[side]={text,path:first[side].path};
    } catch(error) {
      if(!current())return;
      if(error.message===`file is absent on ${side}`)sources[side]={text:'',path:'',absent:true};
      else sources[side]={error:error.message};
    }
  }
  if(!current())return;
  status.remove();
  for(const side of ['before','after']){
    const source=sources[side];
    if(source.absent)el('p',`File is absent on ${side}.`,reader);
    if(source.error)el('p',`${side}: ${source.error}`,reader,'error');
  }
  if(sources.before.error||sources.after.error)el('p','The diff is unavailable because complete source is unavailable. Read the retained source or Git patch below.',reader);
  else {
    const lineCount=(sources.before.text.match(/\n/g)||[]).length+(sources.after.text.match(/\n/g)||[]).length;
    const changes=lineCount<=10000?Diff.diffLines(sources.before.text,sources.after.text,{timeout:1000,maxEditLength:4000}):undefined;
    if(!changes)el('p','The diff exceeds the display limits. Read the retained source or Git patch below.',reader);
    else changeRenderDiff(reader,sources,changes);
  }
  const retained=el('details',undefined,reader);el('summary','Read retained source',retained);
  for(const side of ['before','after']){
    const target=el('section',undefined,retained);el('h4',side==='before'?'Before':'After',target);
    const load=async(offset=0)=>{
      try{
        const result=await changeQuery('source',{fileID,side,textOffset:offset});
        if(!current())return;
        el('small',`${result[side].path}:${result.line}`,target);el('pre',result.text,target);
        if(result.textPage.nextOffset!==null){
          const more=button('Continue retained source',()=>changeAct(async()=>{await load(result.textPage.nextOffset);more.remove();}),target);
        }
      }catch(error){if(current())el('p',error.message,target,'error');}
    };
    button(`Load ${side} retained source`,()=>changeAct(()=>load()),target);
  }
  const patch=async(offset=0)=>{
    const result=await changeQuery('patch',{fileID,textOffset:offset});if(!current())return;el('pre',result.text,reader);
    if(result.textPage.nextOffset!==null){const more=button('Continue patch',()=>changeAct(async()=>{await patch(result.textPage.nextOffset);more.remove();}),reader);}
  };
  button('Read targeted Git patch',()=>changeAct(patch),reader);
}
function changeRenderDiff(reader,sources,changes) {
  el('p','Aligned full-file diff · red: removed · green: added · blank cells have no corresponding line.',reader);
  const viewport=el('div',undefined,reader,'change-diff');viewport.tabIndex=0;
  viewport.setAttribute('role','region');viewport.setAttribute('aria-label','Side-by-side retained source diff');
  const headings=el('div',undefined,viewport,'change-diff-row change-diff-head');
  for(const side of ['before','after'])el('strong',`${side==='before'?'Before':'After'} · ${sources[side].path||'(absent)'}`,headings);
  const languages={go:'go',json:'json',js:'javascript',mjs:'javascript',py:'python',sh:'bash',bash:'bash'};
  const lines=change=>{
    const parts=change.value.split('\n');
    if(parts.at(-1)==='')parts.pop();
    return parts.map((text,index)=>({text,noNewline:index===parts.length-1&&!change.value.endsWith('\n')}));
  };
  let beforeLine=1,afterLine=1;
  const row=(left,right,removed=false,added=false)=>{
    const container=el('div',undefined,viewport,'change-diff-row');
    for(const [side,line,changed] of [['before',left,removed],['after',right,added]]){
      const cell=el('div',undefined,container,`change-diff-cell${changed?' change-diff-'+(side==='before'?'removed':'added'):''}`);
      if(!line){cell.classList.add('change-diff-empty');continue;}
      const number=side==='before'?beforeLine++:afterLine++;
      el('span',String(number),cell,'change-diff-number');
      el('span',changed?(side==='before'?'-':'+'):' ',cell,'change-diff-marker');
      const code=el('code',undefined,cell,'change-diff-code');
      const ext=sources[side].path.split('.').pop().toLowerCase(),language=languages[ext];
      if(language&&line.text.length<=4000)code.innerHTML=Prism.highlight(line.text,Prism.languages[language],language);
      else code.textContent=line.text;
      if(line.noNewline)el('span',' [no final newline]',cell,'change-diff-no-newline');
    }
  };
  for(let i=0;i<changes.length;i++){
    const change=changes[i];
    if(!change.added&&!change.removed){for(const line of lines(change))row(line,line);continue;}
    let removed=[],added=[];
    while(i<changes.length&&(changes[i].added||changes[i].removed)){
      if(changes[i].removed)removed.push(...lines(changes[i]));else added.push(...lines(changes[i]));
      i++;
    }
    i--;
    for(let n=0;n<Math.max(removed.length,added.length);n++)row(removed[n],added[n],!!removed[n],!!added[n]);
  }
}
async function changeMark(action) {
  if(!confirm(action.flowID?'Update this local flow-context consideration?':`Update exactly ${action.itemIDs.length} displayed item IDs to ${action.status}? This is local tracking, not provider approval.`))return;
  await changeRequest('/api/changes/state',{comparisonID:changeState.id,version:changeState.overview.progress.version,...action,confirmed:true});
  await changeRefresh();
  if(changeState.flow){const current=await changeQuery('flows',{flowID:changeState.flow.id});if(current.items[0]){changeState.flow=current.items[0];await changeFlowRecords(changeState.flow);}}
  changeNotice('Local review state saved. Inspecting implementation does not automatically consider every caller context.');
}
async function changePoll() {
  const status=await changeRequest('/api/changes');
  changeState.jobID=status.jobID;
  changeEl('change-cancel').hidden=!status.running;
  if(status.running){
    changeNotice([status.progress?.phase,status.progress?.side,status.progress?.module].filter(Boolean).join(' · '));
    setTimeout(()=>changeAct(changePoll),700);
  }else if(status.error)changeNotice(status.error,true);
  else if(status.comparisonID){changeAdoptComparison(status.comparisonID);changeEl('change-inputs').open=false;await changeRefresh();changeNotice('Pinned comparison ready. Target tests and scripts were not executed.');}
}
changeEl('change-review').onclick=changeOpen;
changeEl('change-return').onclick=changeClose;
changeEl('change-setup').onclick=()=>{changeEl('change-inputs').open=!changeEl('change-inputs').open;};
changeEl('change-back').onclick=()=>changeAct(async()=>{
  const previous=changeState.history.pop();if(!previous){changeClose();return;}
  Object.assign(changeState,previous);
  if(changeState.detailInput)await changeShowSide(changeState.detailInput,changeState.detailLabel,false);
  else if(changeState.inventoryFile)await changeSelectFile(changeState.inventoryFile,false);
  else if(changeState.fileID)await changeCompare(changeState.fileID,changeState.record,false);
  else if(changeState.record)await changeSelectRecord(changeState.record.id,false);
  else if(changeState.flow)await changeRenderFlow();
  else await changeList();
});
for(const control of document.querySelectorAll('[data-change-perspective]'))control.onclick=()=>changePerspective(control.dataset.changePerspective);
changeEl('change-search').oninput=()=>{changeState.query=changeEl('change-search').value;changeState.offset=0;changeAct(changeList);};
changeEl('change-filter').onchange=()=>{changeState.status=changeEl('change-filter').value;changeState.offset=0;changeAct(changeList);};
changeEl('change-input-form').onsubmit=event=>{
  event.preventDefault();changeAct(async()=>{
    const resolved=await changeRequest('/api/changes/resolve',{directory:changeEl('change-directory').value,
      before:changeEl('change-before').value,after:changeEl('change-after').value,remote:changeEl('change-remote').value,mergeBase:changeEl('change-merge-base').checked});
    changeState.preview=resolved;changeRenderPreview(resolved);
  });
};
changeEl('change-confirm').onclick=()=>changeAct(async()=>{
  if(!changeState.preview)throw Error('Resolve revisions first');
  if(!confirm('Collect these exact revisions? Go tooling/dependency preparation may run. No target tests or scripts will execute.'+
    (changeState.id?' The current comparison will be replaced; changed evidence does not inherit completion.':'')))return;
  await changeRequest('/api/changes/collect',{before:changeState.preview.before.commit,after:changeState.preview.after.commit,
    previewID:changeState.preview.previewID,replaceID:changeState.id||undefined,confirmed:true});
  changeEl('change-confirm').hidden=true;await changePoll();
});
changeEl('change-cancel').onclick=()=>changeAct(async()=>{
  if(!confirm('Cancel comparison collection and keep the previous comparison?'))return;
  await changeRequest('/api/changes/cancel',{confirmed:true,jobID:changeState.jobID});await changePoll();
});
const changeOrdinaryRender=render;
render=function(){if(changeState.active)changeClose();return changeOrdinaryRender();};
