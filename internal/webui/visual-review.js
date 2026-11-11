'use strict';
let visualResizeObserver;

function visualMetrics(parent,symbolIDs) {
  const m=GoReviewVisual.metrics(report,symbolIDs),count=m.memberCount;
  const strip=el('div',undefined,parent,'review-metrics');
  strip.title=count+' unique operation definitions; repeated call sites count once. Coverage counts overlapping statement blocks once.';
  const arch=m.architecture,colors={clear:'#159447',watch:'#f4cb32',concern:'#f59b23',
    'high-concern':'#ef4444','not-assessed':'#465367'};
  const architectureOptions={...GoReviewGraph.gaugeOptions('coverage',
    arch.level==='not-assessed'?null:GoReviewAssessment.rank[arch.level]),max:3,
    gaugeColor:colors[arch.level],levelColors:[colors[arch.level]]};
  for(const [kind,value,caption,title,options] of [
    ['coverage',m.coverage.value,m.coverage.value===null?'?':Math.round(m.coverage.value)+'%',
      metricDescription('coverage',m.coverage),null],
    ['complexity',m.complexity.value,m.complexity.value??'?',
      'Maximum cyclomatic complexity across '+count+' unique operation definitions; not a sum. '+m.complexity.unknown+' unmeasured.',null],
    ['architecture',architectureOptions.pointer?architectureOptions.value:null,GoReviewAssessment.labels[arch.level],
      'Highest supported-rule concern across '+count+' unique operation definitions. '+arch.unknown+' not assessed. Not a quality verdict.',architectureOptions]
  ]) {
    const meter=el('span',undefined,strip,'review-meter');meter.dataset.metric=kind;
    if(!parent.closest('button'))meter.tabIndex=0;
    meter.title=title;meter.setAttribute('role','meter');
    meter.setAttribute('aria-label',kind);meter.setAttribute('aria-valuetext',String(caption));
    if(value!==null) {
      meter.setAttribute('aria-valuenow',value);meter.setAttribute('aria-valuemin',kind==='complexity'?1:0);
      meter.setAttribute('aria-valuemax',kind==='coverage'?100:kind==='architecture'?3:Math.max(25,value));
    }
    const dial=svg('svg',{viewBox:'-28 -28 56 34','aria-hidden':'true'},meter);
    drawDial(dial,kind==='architecture'?'signal':kind,{value},options||GoReviewGraph.gaugeOptions(kind,value));
    const label=el('span',undefined,meter);
    el('strong',String(caption),label);
    el('small',kind==='coverage'?'Coverage':kind==='complexity'?'CC · max':'Arch',label);
  }
  if(m.complexity.unknown||arch.unknown)
    el('span',Math.max(m.complexity.unknown,arch.unknown)+' unassessed',strip,'review-unassessed');
  return m;
}

function visualSelect(endpoint,scene,node,open=true) {
  mode={...mode,selected:node.id,inspectorOpen:open||mode.inspectorOpen};
  document.body.classList.toggle('review-inspector-open',!!mode.inspectorOpen);
  for(const card of $('activity-overview').querySelectorAll('.review-node')) {
    const selected=card.dataset.nodeId===node.id;
    card.classList.toggle('selected-activity',selected);
    card.querySelector('.activity-select').setAttribute('aria-pressed',String(selected));
  }
  for(const edge of $('activity-overview').querySelectorAll('.review-connection'))
    edge.classList.toggle('related',edge.dataset.from===node.id||edge.dataset.to===node.id);
  overallStageDetail(endpoint,scene,node);
  const scope=el('section',undefined,$('detail'),'review-scope-evidence');
  el('h3','Measurement scope',scope);visualMetrics(scope,node.symbolIDs||[]);
  el('p',(node.symbolIDs?.length||0)+' unique operation definitions represented by this source-owned group. '+
    'Statement coverage is weighted by statements; CC is the maximum; architecture is the highest assessed concern. '+
    'Unknown members remain unknown. Hygiene is snapshot-wide, shown in the workspace summary.',scope,'muted');
  for(const id of node.symbolIDs||[]) {
    const symbol=sym(id);if(!symbol)continue;
    const row=el('details',undefined,scope);el('summary',label(symbol),row);
    if(symbol.doc)el('p',symbol.doc,row);
    for(const comment of symbol.comments||[])el('p',comment.text,row);
    visualMetrics(row,[id]);systemFlowLocation(symbol.location,row);
  }
}

function visualEdgeDetail(endpoint,scene,edge) {
  mode={...mode,inspectorOpen:true};
  document.body.classList.add('review-inspector-open');
  const from=scene.byID.get(edge.from),to=scene.byID.get(edge.to);
  detail(from.title+' → '+to.title,edge.kind==='asynchronous'?
    'Asynchronous launch; no completion ordering or return join is inferred.':
    'Possible source continuation, not a recorded execution.');
  if(edge.guards.length) {
    el('h3','Source alternatives', $('detail'));
    for(const guard of edge.guards)el('p',guard,$('detail'));
    el('p','These alternatives summarize possible source paths. They are not necessarily one conjunction or one test invocation.',$('detail'),'muted');
  }
  for(const node of [from,to]) {
    button('Select '+node.title,()=>visualSelect(endpoint,scene,node),$('detail'));
    for(const info of node.information||[])
      el('p',(info.direction==='input'?'Input':'Output')+': '+info.type+(info.name?' · '+info.name:''),$('detail'));
  }
  el('p','Information contracts describe source types and names, not runtime values or proven object lineage.',$('detail'),'muted');
  for(const a of edge.alternatives)systemFlowAlternative(a,$('detail'));
}

function visualFocus(node,tree) {
  const overview=GoReviewVisual.project(tree,[]);
  go({...mode,detailLevel:undefined,flowSymbolID:node.symbolID,parentReview:{...mode},
    parentOverview:{entry:overview.entry,nodes:overview.nodes.map(n=>({id:n.id,title:n.title,symbolID:n.symbolID,phase:n.phase})),
      edges:overview.edges.map(e=>({from:e.from,to:e.to,kind:e.kind}))},
    expandedActivities:[],selected:undefined,reviewScroll:undefined,reviewZoom:undefined,reviewPositions:undefined,viewport:undefined});
}

function visualParentOverview(parent) {
  if(!mode.parentOverview)return;
  const overview=mode.parentOverview,section=el('section',undefined,parent,'review-parent-flow');
  el('strong','Parent context',section);
  button('Return to parent flow',()=>go({...mode.parentReview}),section);
  const height=Math.max(52,...overview.nodes.map(n=>Math.ceil(n.title.length/23)*12+10)),rowHeight=height+25;
  const arrangement=GoReviewVisual.layout(overview,overview.nodes.map(n=>n.id));
  const placements=[...arrangement.placements,...arrangement.background.map((id,i)=>({id,column:3,row:i+1}))];
  const width=Math.max(1,...placements.map(p=>p.column))*180;
  const canvas=svg('svg',{viewBox:`0 0 ${width} ${Math.max(1,...placements.map(p=>p.row))*rowHeight+10}`,
    role:'img','aria-label':'Parent flow with current operation highlighted'},section);
  canvas.style.maxWidth=width+'px';
  const marker=svg('marker',{id:'parent-arrow',viewBox:'0 0 10 10',refX:9,refY:5,markerWidth:5,markerHeight:5,orient:'auto'},svg('defs',{},canvas));
  svg('path',{d:'M0 0 L10 5 L0 10z',fill:'#91abc9'},marker);
  const positions=new Map(placements.map(p=>[p.id,{x:10+(p.column-1)*180,y:10+(p.row-1)*rowHeight,w:150,h:height}]));
  for(const edge of overview.edges) {
    const points=GoReviewGraph.route([...positions].map(([id,p])=>({id,...p})),edge.from,edge.to);
    svg('path',{d:points.map((p,i)=>(i?'L':'M')+p.x+' '+p.y).join(' '),
      class:'review-parent-edge'+(edge.kind==='asynchronous'?' asynchronous':''),'marker-end':'url(#parent-arrow)'},canvas);
  }
  for(const n of overview.nodes) {
    const p=positions.get(n.id),g=svg('g',{transform:`translate(${p.x},${p.y})`,
      class:(n.symbolID===mode.flowSymbolID?'parent-current ':'')+
        (arrangement.spine.includes(n.id)?'review-parent-main':''),tabindex:0,role:'button','aria-label':'Return to '+n.title},canvas);
    svg('rect',{width:p.w,height:p.h,rx:4},g);
    // Wrapping preserves the entire label rather than clipping parent identity.
    const words=n.title.match(/.{1,23}/g)||[];
    words.forEach((word,i)=>{svg('text',{x:5,y:14+i*12},g).textContent=word;});
    svg('title',{},g).textContent=n.title;
    g.onclick=()=>go({...mode.parentReview,selected:n.id});
    g.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();g.onclick();}};
  }
  setTimeout(()=>{
    const current=section.querySelector('.parent-current');
    if(!current||!section.isConnected)return;
    section.scrollTop+=current.getBoundingClientRect().top-section.getBoundingClientRect().top-50;
  });
}

function renderVisualReview(endpoint,raw) {
  visualResizeObserver?.disconnect();
  const tree=GoReviewVisual.hierarchy(report,raw),scene=GoReviewVisual.project(tree,mode.expandedActivities||[]);
  overallEndpointScene=scene;
  if(scene.error){detail('Overall flow unavailable',scene.error);return;}
  // Detail consumers use the same source membership as the diagram and gauges.
  for(const n of scene.nodes)n.operations=n.sourceIDs.filter(id=>id!==n.id).map(id=>tree.byID.get(id));
  const panel=$('activity-overview');panel.replaceChildren();
  const context=el('div',undefined,panel,'review-context');
  el('strong',mode.flowSymbolID?'Focused operation · '+(sym(mode.flowSymbolID)?.name||'unavailable'):'Entry-point flow',context);
  el('span','Top to bottom: main source continuation · side branches: alternatives · dashed: async',context);
  visualParentOverview(panel);
  const outline=el('details',undefined,context,'review-outline');
  el('summary','Flow outline · '+tree.roots.length+' groups',outline);
  const orientation=el('div',undefined,outline,'review-orientation');
  orientation.setAttribute('aria-label','Parent flow overview');
  for(const id of tree.roots) {
    const n=tree.byID.get(id),b=button(n.title,()=>{
      const target=scene.nodes.find(v=>v.id===id);visualSelect(endpoint,scene,target);
      const card=[...panel.querySelectorAll('.review-node')].find(card=>card.dataset.nodeId===id);
      if(card) {
        const box=card.getBoundingClientRect(),area=viewport.getBoundingClientRect();
        mode={...mode,reviewScroll:{left:(mode.reviewScroll?.left||0)+box.left-area.left-16,
          top:(mode.reviewScroll?.top||0)+box.top-area.top-16}};
        sizeStage();
      }
    },orientation);
    b.className=n.phase==='async-work'?'orientation-async':'';
  }
  const viewport=el('div',undefined,panel,'review-viewport');
  viewport.tabIndex=0;viewport.setAttribute('aria-label','Interactive flow diagram');
  const stage=el('div',undefined,viewport,'review-stage');
  const board=el('div',undefined,stage,'review-board');
  const connections=svg('svg',{class:'review-edges','aria-label':'Possible control relationships'},board);
  const defs=svg('defs',{},connections),marker=svg('marker',{id:'review-arrow',viewBox:'0 0 10 10',
    refX:9,refY:5,markerWidth:6,markerHeight:6,orient:'auto'},defs);
  svg('path',{d:'M0 0 L10 5 L0 10z',fill:'#91abc9'},marker);
  const columns=el('div',undefined,board,'review-columns'),faces=new Map();
  const primaryEdges=new Set();
  function drawLevel(ids,parent) {
    const arrangement=GoReviewVisual.layout(scene,ids);
    parent.dataset.spine=JSON.stringify(arrangement.spine);
    parent.title='Main lane follows the longest forward source continuation, not a proven successful execution.'+
      (arrangement.ambiguous?' Equally long alternatives exist.':'');
    for(let i=1;i<arrangement.spine.length;i++)
      primaryEdges.add(JSON.stringify([arrangement.spine[i-1],arrangement.spine[i]]));
    for(const placement of arrangement.placements) {
      const card=drawNode(scene.nodes.find(n=>n.id===placement.id),parent);
      card.style.gridColumn=String(placement.column);card.style.gridRow=String(placement.row);
      card.classList.toggle('review-main-path',placement.column===1);
    }
    if(arrangement.background.length) {
      const lane=el('div',undefined,parent,'review-lane review-async-lane');
      lane.style.gridColumn='3';lane.style.gridRow='1 / '+(Math.max(1,...arrangement.placements.map(p=>p.row))+1);
      el('h2','Background · no inferred join',lane);
      for(const id of arrangement.background)drawNode(scene.nodes.find(n=>n.id===id),lane);
    }
    parent.classList.toggle('no-alternatives',!arrangement.placements.some(p=>p.column===2));
  }
  function drawNode(node,parent) {
    const group=el('article',undefined,parent,'review-node'+(node.expanded?' review-boundary':''));
    group.dataset.nodeId=node.id;group.dataset.symbolId=node.symbolID||'';
    const position=mode.reviewPositions?.[node.id];
    if(position)group.style.transform=`translate(${position.x}px,${position.y}px)`;
    const face=el('div',undefined,group,'review-node-face');faces.set(node.id,face);
    el('span',node.phase==='async-work'?'Asynchronous work':node.kind,face,'review-node-kind');
    const select=button(node.title,()=>visualSelect(endpoint,scene,node),face);
    select.className='activity-select';select.title=node.description||node.label;
    select.setAttribute('aria-pressed',String(mode.selected===node.id));
    if(node.componentID)el('span',report.systemFlow.components.find(c=>c.id===node.componentID)?.name||node.componentID,face,'review-owner');
    if(node.description) {
      el('p',node.description.split(/\n\s*\n/)[0],face,'review-purpose');
      if(/\n\s*\n/.test(node.description))
        button('Full commentary',()=>visualSelect(endpoint,scene,node),face).className='review-commentary';
    }
    visualMetrics(face,node.symbolIDs);
    const contract=[...new Set((node.information||[]).filter(i=>
      !['context.Context','net/http.ResponseWriter'].includes(i.type)).map(i=>
      (i.direction==='input'?'In: ':'Out: ')+i.type.replace(/(?:[\w.-]+\/)+/g,'')))];
    if(contract.length) {
      const contractButton=button(contract.join(' · '),()=>visualSelect(endpoint,scene,node),face);
      contractButton.className='review-contract';contractButton.title=node.information.map(i=>i.direction+': '+i.type).join('\n');
    }
    const unknown=node.sourceIDs.filter(id=>{
      const n=tree.byID.get(id);return n.boundary||n.resolution==='unresolved';
    }).length;
    if(unknown)el('span',unknown+' unresolved source boundaries',face,'activity-warning');
    if(node.resolution==='inferred')el('span','Inferred target',face,'review-inference');
    const states=new Map();
    for(const a of node.alternatives)states.set(a.execution,(states.get(a.execution)||0)+1);
    if(states.size)el('span',[...states].map(([s,c])=>c+' '+s).join(' · '),face,'review-branches');
    const actions=el('div',undefined,face,'review-node-actions');
    if(node.children.length) {
      const expand=button(node.expanded?'− Collapse':'＋ Expand '+node.children.length,()=>{
        const expanded=new Set(mode.expandedActivities||[]);
        if(expanded.has(node.id))expanded.delete(node.id);else expanded.add(node.id);
        mode={...mode,expandedActivities:[...expanded],selected:node.id};render();
        [...panel.querySelectorAll('.review-node')].find(n=>n.dataset.nodeId===node.id)?.querySelector('.review-expand')?.focus({preventScroll:true});
      },actions);
      expand.className='review-expand';expand.setAttribute('aria-expanded',String(node.expanded));
      expand.setAttribute('aria-label',(node.expanded?'Collapse ':'Expand ')+node.title);
    }
    if(node.symbolID&&(node.collapsed||node.children.length||node.phase==='async-work'))
      button('Focus flow',()=>visualFocus(node,tree),actions);
    if(node.expanded) {
      const children=el('div',undefined,group,'review-children');
      children.setAttribute('aria-label',node.title+' expanded structure');
      drawLevel(node.children,children);
    }
    return group;
  }
  drawLevel(tree.roots,columns);
  const edgeLayer=svg('g',{},connections);
  function sizeStage() {
    const zoom=mode.reviewZoom||1;
    stage.style.width=board.offsetWidth*zoom+'px';stage.style.height=board.offsetHeight*zoom+'px';
    board.style.transform=`translate(${- (mode.reviewScroll?.left||0)}px,${- (mode.reviewScroll?.top||0)}px) scale(${zoom})`;
  }
  function drawConnections() {
    edgeLayer.replaceChildren();
    const base=board.getBoundingClientRect(),inverse=connections.getScreenCTM?.()?.inverse();
    // Measure only node faces. Expanded parent frames are containment, not
    // obstacles; routing against them would obstruct every parent-child edge.
    const rectangles=[...faces].map(([id,face],i)=>{
      const box=face.getBoundingClientRect();
      const start=inverse?new DOMPoint(box.left,box.top).matrixTransform(inverse):null;
      const end=inverse?new DOMPoint(box.right,box.bottom).matrixTransform(inverse):null;
      return {id,x:start?start.x:box.width?box.left-base.left:20+(i%3)*350,
        y:start?start.y:box.height?box.top-base.top:30+Math.floor(i/3)*250,
        w:start?end.x-start.x:box.width||260,h:start?end.y-start.y:box.height||180};
    });
    const labels=[];
    scene.edges.forEach((edge,i)=>{
      const primary=primaryEdges.has(JSON.stringify([edge.from,edge.to]))&&edge.kind!=='asynchronous';
      const route=GoReviewGraph.route(rectangles,edge.from,edge.to,i,primary?0:(i%3-1)*12);
      const group=svg('g',{class:'review-connection'+(primary?' review-primary-connection':'')+
        (edge.kind==='asynchronous'?' asynchronous':''),
        'data-from':edge.from,'data-to':edge.to,role:'button',tabindex:0,
        'aria-label':tree.byID.get(edge.from).title+' → '+tree.byID.get(edge.to).title+
          (edge.guards.length?' · conditional':'')},edgeLayer);
      const d=route.map((p,j)=>(j?'L':'M')+p.x+' '+p.y).join(' ');
      svg('path',{d,class:'review-edge-hit','data-from':edge.from},group);
      svg('path',{d,class:'review-edge-line','marker-end':'url(#review-arrow)'},group);
      svg('title',{},group).textContent=edge.guards.join('\n')||
        (edge.kind==='asynchronous'?'Async launch; no inferred join':'Possible source continuation');
      const caption=edge.kind==='asynchronous'?'launch':edge.guards.length?'conditional':'';
      if(caption) {
        const pos=GoReviewGraph.labelPosition(route,caption.length*7,rectangles,labels);
        if(pos) {
          labels.push(pos);
          svg('text',{x:pos.x,y:pos.y+12,class:'review-edge-label'},group).textContent=caption;
        }
      }
      group.onclick=()=>visualEdgeDetail(endpoint,scene,edge);
      group.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();group.onclick();}};
    });
  }
  board.style.transform=`scale(${mode.reviewZoom||1})`;sizeStage();
  if(typeof ResizeObserver!=='undefined') {
    visualResizeObserver=new ResizeObserver(()=>{
      if(mode.reviewZoom===undefined&&viewport.clientWidth&&board.offsetWidth)
        setZoom(Math.min(1,Math.max(.75,(viewport.clientWidth-32)/board.offsetWidth)));
      else {sizeStage();drawConnections();}
    });
    visualResizeObserver.observe(board);
  }
  drawConnections();
  const controls=el('div',undefined,context,'review-zoom');
  const setZoom=z=>{
    const old=mode.reviewZoom||1,ratio=z/old;
    mode={...mode,reviewZoom:z,reviewScroll:{
      left:((mode.reviewScroll?.left||0)+viewport.clientWidth/2)*ratio-viewport.clientWidth/2,
      top:((mode.reviewScroll?.top||0)+viewport.clientHeight/2)*ratio-viewport.clientHeight/2}};
    sizeStage();
  };
  button('−',()=>setZoom((mode.reviewZoom||1)*.8),controls).setAttribute('aria-label','Zoom out');
  button('+',()=>setZoom((mode.reviewZoom||1)/.8),controls).setAttribute('aria-label','Zoom in');
  button('Fit overview',()=>{
    const zoom=mode.reviewZoom||1,base=board.getBoundingClientRect();
    const boxes=[...faces.values()].map(face=>face.getBoundingClientRect());
    const x=Math.min(0,...boxes.map(b=>(b.left-base.left)/zoom));
    const y=Math.min(0,...boxes.map(b=>(b.top-base.top)/zoom));
    const w=Math.max(board.offsetWidth||980,...boxes.map(b=>(b.right-base.left)/zoom))-x;
    const h=Math.max(board.offsetHeight||700,...boxes.map(b=>(b.bottom-base.top)/zoom))-y;
    const next=Math.min((viewport.clientWidth-32)/w,(viewport.clientHeight-32)/h);
    mode={...mode,reviewZoom:next,reviewScroll:{left:x*next,top:y*next}};sizeStage();
  },controls);
  button('100%',()=>setZoom(1),controls);
  button('Reset layout',()=>{mode={...mode,reviewPositions:undefined};render();},controls);
  el('span','Scroll to pan · Shift+scroll or pinch to zoom · drag background to pan · drag nodes to rearrange · Alt + arrows moves a focused node',context);
  viewport.addEventListener('wheel',event=>{
    event.preventDefault();
    const zoom=mode.reviewZoom||1;
    const next=GoReviewGraph.wheelViewport({x:(mode.reviewScroll?.left||0)/zoom,
      y:(mode.reviewScroll?.top||0)/zoom,w:viewport.clientWidth/zoom,h:viewport.clientHeight/zoom},
      event,{a:1/zoom,b:0,c:0,d:1/zoom},viewport.clientHeight);
    if(event.shiftKey||event.ctrlKey)setZoom(viewport.clientWidth/next.w);
    else {mode={...mode,reviewScroll:{left:next.x*zoom,top:next.y*zoom}};sizeStage();}
  },{passive:false});
  let moving=null,suppressClick=false;
  const moveNode=(card,x,y)=>{
    const id=card.dataset.nodeId;
    mode={...mode,reviewPositions:{...mode.reviewPositions,[id]:{x,y}}};
    card.style.transform=`translate(${x}px,${y}px)`;drawConnections();
  };
  viewport.addEventListener('pointerdown',event=>{
    if(event.button!==0)return;
    const face=event.target.closest('.review-node-face'),card=face?.closest('.review-node');
    const position=mode.reviewPositions?.[card?.dataset.nodeId]||{x:0,y:0};
    moving={pointer:event.pointerId,card,capture:event.target,x:event.clientX,y:event.clientY,position,
      left:mode.reviewScroll?.left||0,top:mode.reviewScroll?.top||0,moved:false};
    suppressClick=false;
    moving.capture.setPointerCapture?.(event.pointerId);
  });
  viewport.addEventListener('pointermove',event=>{
    if(!moving||event.pointerId!==moving.pointer)return;
    const dx=event.clientX-moving.x,dy=event.clientY-moving.y;
    if(!moving.moved&&Math.hypot(dx,dy)<4)return;
    moving.moved=true;
    if(moving.card)moveNode(moving.card,moving.position.x+dx/(mode.reviewZoom||1),moving.position.y+dy/(mode.reviewZoom||1));
    else {mode={...mode,reviewScroll:{left:moving.left-dx,top:moving.top-dy}};sizeStage();}
  });
  const finish=event=>{
    if(!moving||event.pointerId!==moving.pointer)return;
    suppressClick=moving.moved;
    const capture=moving.capture;moving=null;
    if(capture.hasPointerCapture?.(event.pointerId))capture.releasePointerCapture(event.pointerId);
  };
  for(const type of ['pointerup','pointercancel','lostpointercapture'])viewport.addEventListener(type,finish);
  viewport.addEventListener('click',event=>{
    if(suppressClick){event.preventDefault();event.stopImmediatePropagation();suppressClick=false;}
  },true);
  viewport.addEventListener('keydown',event=>{
    const card=event.target.closest('.review-node');
    if(!event.altKey||!card||!['ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(event.key))return;
    event.preventDefault();event.stopPropagation();
    const position=mode.reviewPositions?.[card.dataset.nodeId]||{x:0,y:0},step=event.shiftKey?40:10;
    moveNode(card,position.x+(event.key==='ArrowRight'?step:event.key==='ArrowLeft'?-step:0),
      position.y+(event.key==='ArrowDown'?step:event.key==='ArrowUp'?-step:0));
  });
  visualSelect(endpoint,scene,scene.nodes.find(n=>n.id===mode.selected)||scene.nodes[0],false);
}

const beforeVisualWorkspaceRender=render;
render=function() {
  const diagram=mode.kind==='application'||mode.kind==='endpoint'&&!mode.detailLevel;
  if(mode.kind!=='endpoint'||mode.detailLevel)visualResizeObserver?.disconnect();
  document.body.classList.toggle('visual-workspace',!!report);
  document.body.classList.toggle('review-diagram',diagram);
  document.body.classList.toggle('review-setup-open',!!mode.reviewSetup);
  document.body.classList.toggle('review-inspector-open',!!mode.inspectorOpen||
    mode.kind==='application'&&mode.inspectorOpen!==false||!!mode.documentOpen&&mode.inspectorOpen!==false||!diagram);
  $('workspace-evidence').setAttribute('aria-pressed',String(document.body.classList.contains('review-inspector-open')));
  $('workspace-setup').setAttribute('aria-expanded',String(!!mode.reviewSetup));
  beforeVisualWorkspaceRender();
};
$('workspace-setup').onclick=()=>{mode={...mode,reviewSetup:!mode.reviewSetup};render();};
$('workspace-evidence').onclick=()=>{
  const open=!document.body.classList.contains('review-inspector-open');
  mode={...mode,inspectorOpen:open};
  document.body.classList.toggle('review-inspector-open',open);
  $('workspace-evidence').setAttribute('aria-pressed',String(open));
};
$('workspace-tools').onclick=()=>{
  $('advanced-tools').open=!$('advanced-tools').open;
  $('workspace-tools').setAttribute('aria-expanded',String($('advanced-tools').open));
};
$('advanced-tools').addEventListener('toggle',()=>{
  $('workspace-tools').setAttribute('aria-expanded',String($('advanced-tools').open));
});
$('system-context').onclick=()=>go({kind:'system-flow',level:'context'});
$('system-components').onclick=()=>go({kind:'system-flow',level:'components'});
// Retained diagnostics and documentation also use the inspector. Their handlers
// are installed by other files, so open it before dispatch without replacing them.
document.addEventListener('click',event=>{
  if(!event.target.closest('#review-results,#scope-results,#evidence,#docs,#run-tests,#import-coverage'))return;
  mode={...mode,inspectorOpen:true};
  document.body.classList.add('review-inspector-open');
  $('workspace-evidence').setAttribute('aria-pressed','true');
},true);
const beforeVisualSearch=$('search').oninput;
$('search').oninput=()=>{
  const query=$('search').value.trim().toLowerCase();
  if(!report||!query){beforeVisualSearch?.();return;}
  mode={...mode,inspectorOpen:true};document.body.classList.add('review-inspector-open');
  detail('Search results','Symbols and collected documentation matching '+$('search').value);
  const symbols=report.symbols.filter(s=>[s.name,s.package,s.doc,s.signature].join(' ').toLowerCase().includes(query));
  for(const s of symbols)button(short(s.package)+' · '+label(s),()=>go({kind:'logical',id:s.id}),$('detail'));
  for(const doc of report.documents.filter(d=>(d.path+' '+d.text).toLowerCase().includes(query)))
    button(doc.path,()=>openDocumentation(doc),$('detail'));
  if(!symbols.length&&!report.documents.some(d=>(d.path+' '+d.text).toLowerCase().includes(query)))
    el('p','No matching symbols or collected documents.',$('detail'));
};
