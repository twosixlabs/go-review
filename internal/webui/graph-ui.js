'use strict';
const layoutMemory=new WeakMap();
let activeScene=null;
function graphKey(){
  return mode.kind+':'+(mode.id||'')+(mode.kind==='symbol'?':'+preferControl+
    (preferControl?':'+$('show-unreachable').checked+':'+$('compact-statements').checked+
      ($('compact-statements').checked?':'+[...(mode.expandedStatements||[])].sort().join(','):''):''):'');
}
function rememberedPositions(key,positions){
  let graphs=layoutMemory.get(report);
  if(!graphs){graphs=new Map();layoutMemory.set(report,graphs);}
  if(!graphs.has(key))graphs.set(key,new Map());
  const saved=graphs.get(key);
  for(const [id,p] of positions)if(saved.has(id))Object.assign(p,saved.get(id));
  return saved;
}
function attachGraph(coords,edges,width,height,key){
  const root=$('graph'),saved=rememberedPositions(key,coords),layer=svg('g',{class:'edge-layer'},root);
  root.insertBefore(layer,root.querySelector('.node'));
  const groups=new Map([...root.querySelectorAll('.node')].map(g=>[g.dataset.nodeId,g]));
  const scene={coords,key,bounds:null,routes:[]};
  function draw(){
    layer.replaceChildren();
    for(const [id,p] of coords)groups.get(id)?.setAttribute('transform',`translate(${p.x},${p.y})`);
    const rectangles=[...coords].map(([id,p])=>({id,...p,w:width,h:height})),points=[],labels=[];
    scene.routes=[];
    edges.forEach((edge,i)=>{
      if(!coords.has(edge.from)||!coords.has(edge.to))return;
      // Separate reciprocal information contracts; shared ports obscure their
      // direction and put both labels on the same line.
      const offset=edge.architecture?(edge.from<edge.to?-16:16):0;
      const route=GoReviewGraph.route(rectangles,edge.from,edge.to,i,offset);if(!route.length)return;
      points.push(...route);scene.routes.push({from:edge.from,to:edge.to,points:route,obstructed:route.obstructed});
      const color=edge.label==='true'?'true':edge.label==='false'?'false':'normal';
      const path=svg('path',{d:route.map((p,j)=>(j?'L':'M')+p.x+' '+p.y).join(' '),
        class:'edge control-edge '+color+(edge.indirect?' indirect':'')+(route.obstructed?' obstructed':'')+
          (edge.kind==='loop'||coords.get(edge.to).y<coords.get(edge.from).y?' backedge':''),
        'marker-end':`url(#scene-arrow-${color})`},layer);
      svg('title',{},path).textContent=(edge.description||edge.label||edge.kind||'Static relationship')+(route.obstructed?' · overlapping nodes obstruct this edge; drag nodes apart':'');
      if(edge.label){
        let length=-1,a,b;
        for(let j=1;j<route.length;j++){
          const n=Math.abs(route[j].x-route[j-1].x)+Math.abs(route[j].y-route[j-1].y);
          if(n>length){length=n;a=route[j-1];b=route[j];}
        }
        if(a){
          const vertical=a.x===b.x,left=edge.architecture&&vertical&&offset<0;
          let x=(a.x+b.x)/2+(left?-10:edge.architecture&&vertical?10:5);
          let y=(a.y+b.y)/2+(edge.architecture&&!vertical&&offset>0?16:-5);
          let anchor=left?'end':edge.architecture&&!vertical?'middle':'start';
          const labelWidth=edge.label.length*7.5;
          const placement=edge.architecture?GoReviewGraph.labelPosition(route,labelWidth,rectangles,labels,offset):null;
          if(placement) {
            labels.push(placement);x=placement.x+placement.w/2;y=placement.y+12;anchor='middle';
          }
          const text=svg('text',{x,y,'text-anchor':anchor,class:'edge-label '+color},layer);text.textContent=edge.label;
          if(edge.architecture) {
            points.push({x:x-(anchor==='end'?labelWidth:anchor==='middle'?labelWidth/2:0),y:y-14},
              {x:x+(anchor==='start'?labelWidth:anchor==='middle'?labelWidth/2:0),y:y+4});
          }
        }
      }
    });
    scene.bounds=GoReviewGraph.bounds(rectangles,points);
    if(typeof flowBounds!=='undefined')flowBounds=scene.bounds;
  }
  const defs=root.querySelector('defs')||svg('defs',{},root);
  for(const [name,color] of [['normal','#91abc9'],['true','#8cdbb0'],['false','#f5bd81']]){
    const marker=svg('marker',{id:'scene-arrow-'+name,viewBox:'0 0 10 10',refX:9,refY:5,markerWidth:6,markerHeight:6,orient:'auto'},defs);
    svg('path',{d:'M0 0 L10 5 L0 10z',fill:color},marker);
  }
  for(const [id,group] of groups){
    let moving=null,suppressClick=false;
    group.addEventListener('pointerdown',event=>{
      if(event.button!==0)return;
      event.stopPropagation();
      const matrix=root.getScreenCTM();if(!matrix)return;
      const point=new DOMPoint(event.clientX,event.clientY).matrixTransform(matrix.inverse()),p=coords.get(id);
      moving={pointer:event.pointerId,point,x:p.x,y:p.y,clientX:event.clientX,clientY:event.clientY,moved:false};
      suppressClick=false;group.setPointerCapture(event.pointerId);
    });
    group.addEventListener('pointermove',event=>{
      if(!moving||event.pointerId!==moving.pointer)return;
      if(!moving.moved&&Math.hypot(event.clientX-moving.clientX,event.clientY-moving.clientY)<4)return;
      moving.moved=true;
      const point=new DOMPoint(event.clientX,event.clientY).matrixTransform(root.getScreenCTM().inverse());
      const p=coords.get(id);p.x=moving.x+point.x-moving.point.x;p.y=moving.y+point.y-moving.point.y;
      saved.set(id,{...p});draw();
    });
    const finish=event=>{
      if(!moving||event.pointerId!==moving.pointer)return;
      suppressClick=moving.moved;moving=null;
      if(group.hasPointerCapture(event.pointerId))group.releasePointerCapture(event.pointerId);
    };
    group.addEventListener('pointerup',finish);group.addEventListener('pointercancel',finish);group.addEventListener('lostpointercapture',finish);
    group.addEventListener('click',event=>{if(suppressClick){event.preventDefault();event.stopImmediatePropagation();suppressClick=false;}},true);
    group.addEventListener('keydown',event=>{
      if(!event.altKey||!['ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(event.key))return;
      event.preventDefault();event.stopPropagation();
      const p=coords.get(id),step=event.shiftKey?40:10;
      p.x+=event.key==='ArrowRight'?step:event.key==='ArrowLeft'?-step:0;
      p.y+=event.key==='ArrowDown'?step:event.key==='ArrowUp'?-step:0;
      saved.set(id,{...p});draw();
    });
  }
  draw();activeScene=scene;
  return scene;
}
function focusGraphNode(id){
  const p=activeScene?.coords.get(id);
  if(p){pan={x:p.x-40,y:p.y-80,w:650,h:600};view();}
}
function gaugeMetric(kind,location,value){
  return kind==='coverage'?GoReviewGraph.coverageMetric(location,report.coverage):GoReviewGraph.complexityMetric(value);
}
function packageLocations(path){
  return report.files.filter(f=>f.package===path).map(f=>({fileID:f.id,line:1,column:1,endLine:Number.MAX_SAFE_INTEGER,endColumn:1}));
}
function metricDescription(kind,m){
  if(kind==='coverage')return m.value===null?'No matching statement coverage evidence':
    `${m.covered}/${m.total} overlapping statements executed; ${report.coverage?.sourceVerified?'source verified':'imported; freshness unverified'}. Statement coverage, not branch coverage. The full face shows status color: red at 0%, orange at 40%, yellow at 60%, green at 80%+, blended between anchors.`;
  return 'Cyclomatic complexity: 1 + syntax decisions. The full face shows status color: green at ≤5, yellow at 10, orange at 20, red at 25+, blended between anchors. Needle saturates at 25; number remains actual. Not a quality verdict.';
}
function metricGauge(parent,kind,location,value){
  const m=gaugeMetric(kind,location,value),label=kind==='coverage'?'Coverage':'Complexity',formatted=m.value===null?'?':kind==='coverage'?m.value.toFixed(1)+'%':String(m.value);
  const group=el('span',undefined,parent,'metric-gauge tone-'+m.tone);
  group.tabIndex=0;
  group.setAttribute('role','meter');group.setAttribute('aria-label',label);
  if(m.value!==null){group.setAttribute('aria-valuenow',m.value);group.setAttribute('aria-valuemin',kind==='coverage'?0:1);group.setAttribute('aria-valuemax',kind==='coverage'?100:Math.max(25,m.value));}
  group.setAttribute('aria-valuetext',formatted);group.title=metricDescription(kind,m);
  const canvas=svg('svg',{viewBox:'-28 -28 56 34',class:'metric-dial','aria-hidden':'true'},group);
  drawDial(canvas,kind,m);
  if(m.value!==null)group.style.setProperty('--metric-color',canvas.style.getPropertyValue('--metric-color'));
  const caption=el('span',undefined,group,'metric-caption');
  el('strong',formatted,caption);el('span',label,caption,'metric-label');
  const summary=parent.closest('#detail')?.querySelector(':scope > .context-signal-summary');
  if(summary&&(parent.id==='detail'||parent.parentElement?.id==='detail'))
    summary.insertBefore(group,summary.querySelector('.signal-metric'));
  return group;
}
function drawDial(parent,kind,m,options=GoReviewGraph.gaugeOptions(kind,m.value)){
  if(m.value!==null)parent.style.setProperty('--metric-color',options.gaugeColor);
  const holder=svg('g',{},parent);
  new JustGage({...options,parentNode:holder});
  const widget=holder.querySelector('svg');
  // Crop the widget's label margins; captions and accessible metric values live outside it.
  for(const [key,value] of Object.entries({x:-28,y:-28,width:56,height:34,viewBox:'8 -4 104 60','aria-hidden':'true'}))widget.setAttribute(key,value);
}
function nodeGauges(group,location,complexity,y,width=320){
  for(const [i,kind] of ['coverage','complexity'].entries()){
    if(kind==='complexity'&&!Number.isFinite(complexity))continue;
    const m=gaugeMetric(kind,location,complexity),x=25+i*76,g=svg('g',{class:'node-gauge tone-'+m.tone,transform:`translate(${x},${y-6}) scale(.5)`},group);
    drawDial(g,kind,m);
    svg('text',{x:34,y:0,class:'dial-value'},g).textContent=m.value===null?'?':kind==='coverage'?Math.round(m.value)+'%':m.value;
    svg('text',{x:34,y:16,class:'dial-label'},g).textContent=kind==='coverage'?'Cov':'CC';
    svg('title',{},g).textContent=metricDescription(kind,m);
  }
}
$('fit').onclick=()=>{if(activeScene&&$('graph').querySelector('.node')){pan={...activeScene.bounds};view();}};
$('reset-layout').onclick=()=>{
  const graphs=layoutMemory.get(report);
  if(activeScene)graphs?.delete(activeScene.key);
  render();
};
