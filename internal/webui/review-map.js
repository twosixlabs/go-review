'use strict';
const reviewModels=new WeakMap(),reviewBounds={x:0,y:0,w:1100,h:620};

function renderReviewMap() {
  if(!reviewModels.has(report))reviewModels.set(report,GoReviewMap.reviewRecords(report));
  const model=reviewModels.get(report),plot=GoReviewMap.reviewPlot(model.records,mode);
  const cohort=plot.groups.find(g=>g.key===mode.cohort);
  const selection=plot.records.find(r=>r.symbol.id===mode.id);
  const root=$('graph');root.replaceChildren();$('list').replaceChildren();updateCoverageSummary();
  for(const id of ['function-flow','data-flow','call-map'])$(id).setAttribute('aria-pressed','false');
  $('compact-statements').disabled=true;
  $('breadcrumb').textContent=report.module+' / review map';
  $('legend').textContent='Coverage ↑ · complexity → · number = coincident functions · corner dot = architecture concern';
  const text=(x,y,label,cls='review-axis')=>{svg('text',{x,y,class:cls},root).textContent=label;};
  for(const value of [0,25,50,75,100]) {
    const y=460-value*4;
    svg('line',{x1:100,y1:y,x2:1040,y2:y,class:'review-grid'},root);
    text(50,y+5,value+'%');
  }
  text(100,30,'Statement coverage');
  text(450,610,'Cyclomatic complexity (raw count)');
  for(let i=0;i<=4;i++) {
    const value=1+(plot.maxComplexity-1)*i/4,x=100+940*i/4;
    svg('line',{x1:x,y1:60,x2:x,y2:460,class:'review-grid'},root);
    text(x-8,488,Number(value.toFixed(2)).toString());
  }
  svg('rect',{x:80,y:510,width:980,height:60,rx:10,class:'review-unknown'},root);
  text(100,505,'Unknown coverage — not zero');
  for(const group of plot.groups) {
    const record=group.members[0],value=record.coverage.value,count=group.members.length;
    const label=count===1?record.symbol.qualified||record.symbol.name:count+' functions at the same measurements';
    const description=label+' · coverage '+(value===null?'unknown':value.toFixed(1)+'%')+' · complexity '+record.complexity;
    const selected=group.key===mode.cohort||group.members.some(r=>r.symbol.id===mode.id);
    const marker=svg('g',{class:'node review-point'+(selected?' selected-point':''),transform:`translate(${group.x},${group.y})`,
      'data-cohort':group.key,tabindex:0,role:'button','aria-label':description},root);
    svg('circle',{r:5+Math.min(11,Math.log2(count)*2),fill:GoReviewGraph.gaugeOptions('coverage',value).gaugeColor},marker);
    if(count>1)svg('text',{'text-anchor':'middle',y:3,class:'review-count'},marker).textContent=count;
    svg('title',{},marker).textContent=description;
    GoReviewAssessment.renderMarkerIndicator(marker,report,group.members.map(r=>({symbolID:r.symbol.id})),
      5+Math.min(11,Math.log2(count)*2));
    const activate=()=>{
      mode={...mode,id:count===1?record.symbol.id:undefined,cohort:group.key,page:0,viewport:{...pan}};
      render();
    };
    marker.onclick=activate;
    marker.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();activate();}};
  }
  if(!plot.records.length)text(300,270,'No measured functions match these filters.');
  activeScene={bounds:{...reviewBounds},coords:new Map()};
  pan={...(mode.viewport||reviewBounds)};view();

  el('h3','Filter functions',$('list'));
  const packageLabel=el('label','Package',$('list'));packageLabel.htmlFor='review-package';
  const packages=el('select',undefined,$('list'));packages.id='review-package';
  const option=el('option','All packages',packages);option.value='';
  for(const path of [...new Set(model.records.map(r=>r.symbol.package))].sort()){
    const item=el('option',short(path),packages);item.value=path;
  }
  packages.value=mode.package||'';
  const nameLabel=el('label','Name',$('list'));nameLabel.htmlFor='review-name';
  const name=el('input',undefined,$('list'));name.id='review-name';name.type='search';name.value=mode.query||'';
  name.placeholder='Function or qualified name';
  const coverageLabel=el('label','Coverage availability',$('list'));coverageLabel.htmlFor='review-coverage';
  const coverage=el('select',undefined,$('list'));coverage.id='review-coverage';
  for(const [value,title] of [['all','All coverage states'],['known','Known coverage'],['unknown','Unknown coverage']]) {
    const option=el('option',title,coverage);option.value=value;
  }
  coverage.value=mode.coverage||'all';
  const minimumLabel=el('label','Minimum complexity',$('list'));minimumLabel.htmlFor='review-min-complexity';
  const minimum=el('input',undefined,$('list'));minimum.id='review-min-complexity';minimum.type='number';minimum.min=0;
  minimum.value=mode.minComplexity??'';
  const maximumLabel=el('label','Maximum known coverage (%)',$('list'));maximumLabel.htmlFor='review-max-coverage';
  const maximum=el('input',undefined,$('list'));maximum.id='review-max-coverage';maximum.type='number';maximum.min=0;maximum.max=100;
  maximum.value=mode.maxCoverage??'';
  const filter=()=>{
    if(!minimum.checkValidity()||!maximum.checkValidity()){minimum.reportValidity();maximum.reportValidity();return;}
    mode={kind:'review',package:packages.value,query:name.value,coverage:coverage.value,
      minComplexity:minimum.value===''?undefined:Number(minimum.value),
      maxCoverage:maximum.value===''?undefined:Number(maximum.value),viewport:{...pan}};render();
  };
  packages.onchange=filter;
  coverage.onchange=filter;minimum.onchange=filter;maximum.onchange=filter;
  name.oninput=()=>{
    const start=name.selectionStart,end=name.selectionEnd;filter();
    $('review-name').focus();$('review-name').setSelectionRange(start,end);
  };
  el('p',`${plot.records.length} functions · ${plot.groups.length} markers`,$('list'),'muted');
  if(cohort)button('Show all filtered functions',()=>{
    mode={...mode,cohort:undefined,page:0,viewport:{...pan}};render();
  },$('list'));
  const records=[...(cohort?cohort.members:plot.records)].sort(GoReviewMap.compareRecords);
  const pageSize=30,page=Math.min(mode.page||0,Math.max(0,Math.ceil(records.length/pageSize)-1)),start=page*pageSize;
  el('h3',cohort?'At this marker':'Functions · complexity descending',$('list'));
  for(const record of records.slice(start,start+pageSize)) {
    const s=record.symbol;
    const item=button(s.name,()=>{mode={...mode,id:s.id,viewport:{...pan}};render();},$('list'));
    item.title=s.qualified;
    el('span',short(s.package),item,'review-package-name');
    const metrics=el('span',undefined,item,'review-list-metrics');
    el('span','Cov '+(record.coverage.value===null?'?':Math.round(record.coverage.value)+'%'),metrics);
    el('span','CC '+record.complexity,metrics);
  }
  if(records.length)el('p',`${start+1}–${Math.min(start+pageSize,records.length)} / ${records.length}`,$('list'),'muted');
  for(const [label,next] of [['Previous functions',page-1],['Next functions',page+1]])if(next>=0&&next*pageSize<records.length)
    button(label,()=>{mode={...mode,page:next,viewport:{...pan}};render();},$('list'));

  if(selection) {
    const s=selection.symbol;detail(s.name,short(s.package));
    metricGauge($('detail'),'coverage',s.location);
    metricGauge($('detail'),'complexity',null,s.cyclomatic);
    const drill=next=>{mode={...mode,viewport:{...pan}};go(next);};
    button('Explore logical flow',()=>drill({kind:'logical',id:s.id}),$('detail'));
    button('Explore control flow',()=>{preferControl=true;drill({kind:'symbol',id:s.id});},$('detail'));
    button('Explore value data flow',()=>drill({kind:'dataflow',id:s.id}),$('detail'));
    unitEvidence(s);
    const docs=el('details',undefined,$('detail'));el('summary','Documentation & source',docs);
    el('p',s.doc||'No Go documentation on this declaration.',docs);el('pre',s.signature,docs);
    button('Inspect source',()=>source(s.location),docs);
  } else {
    detail(cohort?cohort.members.length+' functions at this marker':'Review map');
    el('p',cohort?'Select a member in the function list. Coincident measurements share a marker; no functions are hidden behind one another.':
      'Select a marker or function to inspect evidence. Larger numbered markers contain functions with identical measurements.',$('detail'));
  }
  el('p',`${model.unmeasured} callable declarations omitted: no measured complexity. Types and variables are not plotted.`,$('detail'),'muted');
  el('p',report.coverage?'Coverage includes only matching profile statements. '+(report.coverage.sourceVerified?'Source verified.':'Imported profile freshness is unverified.'):
    'No coverage profile loaded. All functions appear in the unknown band.',$('detail'),'muted');
  el('p','Measured evidence, not a quality ranking. Statement coverage is not branch coverage or assertion quality. Shared source ranges (including closures) are not independent coverage measurements.',$('detail'),'muted');
}

const beforeReviewRender=render;
const beforeReviewView=view;
view=function(){
  beforeReviewView();
  if(mode.kind==='review')mode={...mode,viewport:{...pan}};
};
render=function(){
  if(!report)return;
  const reviewing=mode.kind==='review';
  $('review-map').setAttribute('aria-pressed',String(reviewing));
  document.querySelector('.graph-hint').textContent=reviewing?
    'Scroll to pan · Shift+scroll or pinch to zoom · points stay at measured coordinates':
    'Scroll to pan · Shift+scroll or pinch to zoom · drag nodes · Alt + arrows moves a focused node';
  if(reviewing)renderReviewMap();else beforeReviewRender();
};
$('review-map').onclick=()=>{if(report)go({kind:'review'});};
for(const id of ['function-flow','data-flow','call-map']) {
  const previous=$(id).onclick;
  $(id).onclick=()=>{
    if(mode.kind!=='review'){previous();return;}
    if(!mode.id){detail('Select a function first','Choose a function in the review map or its list.');return;}
    if(id==='data-flow')go({kind:'dataflow',id:mode.id});
    else {preferControl=id==='function-flow';go({kind:'symbol',id:mode.id});}
  };
}
const beforeReviewFit=$('fit').onclick;
$('fit').onclick=()=>{
  if(mode.kind==='review'){pan={...reviewBounds};view();}
  else beforeReviewFit();
};
const beforeReviewReset=$('reset-layout').onclick;
$('reset-layout').onclick=()=>{
  if(mode.kind==='review'){mode={...mode,viewport:undefined};render();}
  else beforeReviewReset();
};
const beforeReviewSearch=$('search').oninput;
$('search').oninput=()=>{
  if(mode.kind==='review'&&$('search').value.trim())go({kind:'packages'});
  else beforeReviewSearch();
};
