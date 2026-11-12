'use strict';
const qualityState={active:false,id:null,overview:null,selected:null,group:null,source:null,serial:0,timer:null};
const qualityEl=id=>document.getElementById(id);
function qualityNotice(text,error=false) {
  qualityEl('quality-notice').textContent=text;qualityEl('quality-notice').classList.toggle('error',error);
}
async function qualityAct(action) {
  try {return await action();}catch(error){qualityNotice(error.message,true);}
}
async function qualityRequest(path,body,signal) {
  const response=await fetch(path,{...(body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),signal});
  const result=await response.json();if(!response.ok)throw Error(result.error||'Test Quality request failed');return result;
}
function qualityQuery(operation,args={}) {
  return qualityRequest('/api/test-quality/query',{assessmentID:qualityState.id,operation,...args});
}
const qualityScore=value=>value===null||value===undefined?'Unknown':value.toFixed(1)+' / 100';
function qualityCounts(summary) {
  const c=summary?.counts||{};
  return `${c.scored||0} scored · ${c.insufficient||0} insufficient · ${c.unreviewed||0} unreviewed · ${c.excluded||0} excluded`;
}
function qualityContext() {
  return {assessmentID:qualityState.id,operation:qualityState.source?'source':qualityState.selected?'responsibility':'overview',
    ...(qualityState.source||qualityState.selected||{})};
}
function qualityClose() {
  qualityState.active=false;qualityState.serial++;
  qualityEl('test-quality-workspace').hidden=true;document.body.classList.remove('test-quality-open');
  qualityEl('test-quality').setAttribute('aria-pressed','false');workspaceNavigation();
}
function qualityActivate() {
  changeClose();
  qualityState.active=true;qualityEl('test-quality-workspace').hidden=false;
  document.body.classList.add('test-quality-open');document.body.append(qualityEl('chat-panel'));
  qualityEl('test-quality').setAttribute('aria-pressed','true');workspaceNavigation();
}
async function qualityOpen() {
  qualityActivate();
  await qualityAct(async()=>{
    const config=await qualityRequest('/api/chat/status');
    qualityEl('quality-provider').textContent=config.enabled?
      `${config.external?'External':'Local'} provider: ${config.model} · ${config.baseURL}`:'AI disabled. Configure the review assistant provider to start an assessment. Saved results remain readable.';
    qualityState.external=!!config.external;
    qualityEl('quality-external').hidden=!config.external;qualityEl('quality-start').disabled=!config.enabled;
    await qualityPoll();
    if(qualityState.id){await qualityOverview();await qualityPackages();}
    else qualityNotice('No saved assessment for this project. Start explicitly to review retained evidence.');
  });
}
async function qualityPoll() {
  // Polling belongs to the job UI, not the currently displayed workspace.
  // Leaving this view preserves the pinned assessment and does not cancel it.
  clearTimeout(qualityState.timer);
  const response=await qualityRequest('/api/test-quality/status');
  const list=response.assessments||[],select=qualityEl('quality-assessments');
  qualityState.latest=list[0]||null;qualityState.loadedReport=report;qualityApplicationScore(false);
  select.replaceChildren();
  for(const a of list) {
    const option=el('option',`${a.createdAt||a.id} · ${a.status} · ${a.model||''}`,select);
    option.value=a.id;
  }
  if(!qualityState.id&&list.length)qualityState.id=list[0].id;
  if(qualityState.id&&!list.some(a=>a.id===qualityState.id)) {
    const option=el('option','Pinned assessment · '+qualityState.id,select);option.value=qualityState.id;
  }
  select.value=qualityState.id||'';
  const active=list.filter(a=>['running','queued'].includes(a.status));
  qualityEl('test-quality').textContent=active.length?'Test Quality · '+active.length+' active':'Test Quality';
  const current=list.find(a=>a.id===qualityState.id);
  qualityEl('quality-cancel').hidden=!current||!['running','queued'].includes(current.status);
  if(current)qualityEl('quality-progress').textContent=current.status+' · '+qualityCounts(current.summary);
  if(qualityState.active&&qualityState.id)await qualityOverview();
  if(active.length)qualityState.timer=setTimeout(()=>qualityAct(qualityPoll),1200);
}
async function qualityOverview() {
  const id=qualityState.id,data=await qualityQuery('overview');
  if(id!==qualityState.id)return;
  qualityState.overview=data;
  const parent=qualityEl('quality-overview');parent.replaceChildren();
  const score=el('div',undefined,parent,'quality-score');el('span','Overall AI-assessed score',score);el('strong',qualityScore(data.summary?.score),score);
  el('span',data.summary?.partial?'Partial scope':'Assessed scope',score);
  if(data.summary?.score===null)el('p','Unavailable: no functions have supported ratings.',score);
  for(const [name,value] of Object.entries(data.categories||{})) {
    const card=el('div',undefined,parent,'quality-score');el('span',name.replaceAll('-',' '),card);el('strong',qualityScore(value),card);
  }
  qualityEl('quality-progress').textContent=`${data.status} · ${qualityCounts(data.summary)} · ${data.moduleCount||0} modules · ${data.unavailableModuleCount||0} unavailable`;
  const limits=qualityEl('quality-limitations');limits.replaceChildren();
  el('p',`${data.version} · ${data.model} · ${data.root||''} · assessment ${data.assessmentID}`,limits);
  el('p',`Pinned evidence from ${data.createdAt||'this saved assessment'}. It may differ from the currently open source; results never substitute live files.`,limits);
  for(const line of data.limitations||[])el('p',line,limits);
  if(data.unavailableModuleCount)button('Read unavailable modules',()=>qualityAct(async()=>{
    const target=el('div',undefined,limits);await qualityPage('unavailable',{},target,(text)=>el('p',text,target));
  }),limits);
  if(data.error)qualityNotice(data.error,true);
}
async function qualityPage(operation,args,parent,draw,offset=0) {
  const id=qualityState.id,serial=qualityState.serial;
  const data=await qualityQuery(operation,{...args,offset,limit:20});
  if(id!==qualityState.id||serial!==qualityState.serial)return;
  for(const item of data.items||[])draw(item);
  if(data.nextOffset!==null&&data.nextOffset!==undefined) {
    const more=button('More '+operation,()=>qualityAct(async()=>{more.disabled=true;await qualityPage(operation,args,parent,draw,data.nextOffset);more.remove();}),parent);
  }
  if(!offset&&!data.items?.length)el('p','No matching '+operation+'.',parent);
}
async function qualityPackages() {
  qualityState.serial++;
  const parent=qualityEl('quality-packages');parent.replaceChildren();
  button('All project responsibilities',()=>qualityAct(()=>{qualityState.group=null;return qualityFunctions();}),parent);
  await qualityPage('modules',{},parent,g=>{
    const b=button(`${g.module} · ${g.package} · ${qualityScore(g.summary.score)} · ${g.findings} findings`,()=>qualityAct(async()=>{
      qualityState.group={snapshotID:g.snapshotID,module:g.module,package:g.package};await qualityFunctions();
    }),parent);b.title=qualityCounts(g.summary);
  });
}
async function qualityFunctions() {
  qualityState.serial++;
  const parent=qualityEl('quality-functions');parent.replaceChildren();
  qualityState.selected=null;qualityState.source=null;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();
  await qualityPage('responsibilities',{...qualityState.group,query:qualityEl('quality-search').value,status:qualityEl('quality-filter').value},parent,item=>{
    button(`${item.name} · ${item.status} · ${qualityScore(item.summary.score)} · ${item.findingCount} findings`,
      ()=>qualityAct(()=>qualityResponsibility(item.identity)),parent);
  });
}
function qualityCitations(parent,citations) {
  for(const c of citations||[])button(`${c.fileID}:${c.startLine}–${c.endLine}`,()=>qualityAct(()=>qualitySource(c)),parent).className='quality-citation';
}
async function qualityResponsibility(identity) {
  const input={snapshotID:identity.snapshotID,symbolID:identity.symbolID};
  const id=qualityState.id,serial=++qualityState.serial;
  const data=await qualityQuery('responsibility',input);
  if(id!==qualityState.id||serial!==qualityState.serial)return;
  qualityState.selected=input;qualityState.source=null;qualityEl('quality-source').replaceChildren();
  const parent=qualityEl('quality-detail');parent.replaceChildren();
  el('h3',data.name,parent);el('p',data.status+(data.reason?' · '+data.reason:''),parent);
  for(const r of data.ratings||[]) {
    el('h4',`${r.dimension.replaceAll('-',' ')} · ${r.state==='assessed'?r.value+' / 4':r.state}`,parent);
    el('p',r.rationale,parent);qualityCitations(parent,r.evidence);
  }
  for(const f of data.findings||[]) {
    const section=el('section',undefined,parent);el('h4',f.title,section);el('small',f.category+' · AI '+f.kind,section);
    el('p',f.rationale,section);el('p','Consequence: '+f.consequence,section);el('p','Suggested test/improvement: '+f.suggestion,section);qualityCitations(section,f.evidence);
  }
  const details=el('details',undefined,parent);el('summary','Inspect retained tests, coverage & behavior evidence',details);
  button('Browse retained source & test files',()=>qualityAct(async()=>{
    const target=el('div',undefined,details);
    const load=async(offset=0)=>{
      const catalog=await qualityQuery('evidence',{snapshotID:input.snapshotID,evidenceOperation:'catalog',args:{offset,limit:20}});
      for(const f of catalog.items)button(f.path,()=>qualityAct(()=>qualitySource({snapshotID:input.snapshotID,fileID:f.id,startLine:1,endLine:Math.min(f.lines,100)})),target);
      if(catalog.nextOffset!==null)button('More files',()=>qualityAct(()=>load(catalog.nextOffset)),target);
    };await load();
  }),details);
  for(const [label,operation,args] of [
    ['Coverage & test runs','evidence',{symbolID:input.symbolID}],
    ['Executed & uncovered statement blocks','coverage',{symbolID:input.symbolID,limit:100}],
    ['Control-flow alternatives','evidence',{symbolID:input.symbolID,section:'controlFlowNodes'}],
    ['Data flow & opaque boundaries','dataflow',{symbolID:input.symbolID}]
  ])button(label,()=>qualityAct(()=>qualityEvidence(input.snapshotID,operation,args,details)),details);
}
async function qualityEvidence(snapshotID,operation,args,parent) {
  const id=qualityState.id,serial=qualityState.serial;
  const data=await qualityQuery('evidence',{snapshotID,evidenceOperation:operation,args});
  if(id!==qualityState.id||serial!==qualityState.serial||!parent.isConnected)return;
  const target=el('section',undefined,parent);
  el('pre',JSON.stringify(data,null,2),target);
  for(const section of data.sections||[])button('Read '+section,()=>qualityAct(()=>
    qualityEvidence(snapshotID,operation,{...args,section,offset:0},target)),target);
  const pages=[data,data.blocks,data.unitEvidence?.runs,data.observations].filter(p=>Array.isArray(p?.items));
  for(const page of pages) {
    if(page.nextOffset!==null&&page.nextOffset!==undefined)
      button('More evidence',()=>qualityAct(()=>qualityEvidence(snapshotID,operation,{...args,offset:page.nextOffset},target)),target);
    if(operation==='evidence'&&(args.section==='runs'||page===data.unitEvidence?.runs))for(const run of page.items) {
      if(Number.isInteger(run.index))button('Inspect test run '+run.index,()=>qualityAct(()=>
        qualityEvidence(snapshotID,'evidence',{symbolID:args.symbolID,runIndex:run.index},target)),target);
    }
  }
}
async function qualitySource(citation) {
  const input={snapshotID:citation.snapshotID,fileID:citation.fileID,startLine:citation.startLine,endLine:citation.endLine};
  const id=qualityState.id,serial=++qualityState.serial,data=await qualityQuery('source',input);
  if(id!==qualityState.id||serial!==qualityState.serial)return;
  qualityState.source=input;
  const parent=qualityEl('quality-source');parent.replaceChildren();
  button('Return to assessment',()=>{qualityState.source=null;parent.replaceChildren();qualityEl('quality-detail').scrollIntoView?.({block:'nearest'});},parent);
  el('h3',`Retained ${data.path}:${data.startLine}–${data.endLine}`,parent);
  el('p',`Snapshot ${input.snapshotID} · SHA-256 ${data.sha256}`,parent);
  el('pre',data.text.split('\n').map((line,i)=>(data.startLine+i)+'  '+line).join('\n'),parent);
  if(data.startLine>1)button('Read preceding context',()=>qualityAct(()=>qualitySource({...input,startLine:Math.max(1,data.startLine-100),endLine:data.startLine-1})),parent);
  if(data.endLine<data.totalLines)button('Read following context',()=>qualityAct(()=>qualitySource({...input,startLine:data.endLine+1,endLine:Math.min(data.totalLines,data.endLine+100)})),parent);
}
async function qualityNavigate(input,expectedFingerprint,signal) {
  const check=()=>{signal?.throwIfAborted();if(expectedFingerprint!==undefined&&chatFingerprint()!==expectedFingerprint)throw Error('View changed manually; automatic Test Quality navigation cancelled.');};
  check();await qualityRequest('/api/test-quality/query',input,signal);check();
  if(qualityState.id!==input.assessmentID){
    qualityState.id=input.assessmentID;qualityState.selected=null;qualityState.source=null;qualityState.group=null;
    qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();
  }
  qualityActivate();
  if(expectedFingerprint!==undefined)expectedFingerprint=chatFingerprint();
  await qualityOpen();
  check();
  if(input.operation==='responsibility')await qualityResponsibility(input);
  else if(input.operation==='source')await qualitySource(input);
  else {qualityState.selected=null;qualityState.source=null;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();}
}
qualityEl('test-quality').onclick=qualityOpen;
qualityEl('quality-return').onclick=qualityClose;
qualityEl('quality-start').onclick=()=>qualityAct(async()=>{
  if(qualityState.external&&!qualityEl('quality-consent').checked)throw Error('Approve the external-provider disclosure before starting.');
  const a=await qualityRequest('/api/test-quality/start',{confirmExternal:qualityState.external&&qualityEl('quality-consent').checked});
  qualityState.id=a.id;qualityState.selected=null;qualityState.source=null;qualityState.group=null;qualityState.serial++;
  qualityEl('quality-consent').checked=false;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();
  qualityNotice('Assessment started in the background. Continue browsing or chatting; only Cancel stops it.');
  await qualityPoll();await qualityPackages();await qualityFunctions();
});
qualityEl('quality-cancel').onclick=()=>qualityAct(async()=>{await qualityRequest('/api/test-quality/cancel',{assessmentID:qualityState.id});await qualityPoll();});
qualityEl('quality-refresh').onclick=()=>qualityAct(async()=>{await qualityPoll();if(qualityState.id){await qualityPackages();await qualityFunctions();}});
qualityEl('quality-assessments').onchange=()=>qualityAct(async()=>{
  qualityState.id=qualityEl('quality-assessments').value;qualityState.group=null;qualityState.serial++;
  await qualityOverview();await qualityPackages();await qualityFunctions();
});
qualityEl('quality-search').oninput=()=>qualityAct(qualityFunctions);
qualityEl('quality-filter').onchange=()=>qualityAct(qualityFunctions);
qualityEl('quality-findings-panel').ontoggle=()=>{
  if(qualityEl('quality-findings-panel').open&&qualityState.id)qualityAct(qualityFindings);
};
async function qualityFindings() {
  const parent=qualityEl('quality-findings');parent.replaceChildren();
  await qualityPage('findings',{category:qualityEl('quality-category').value},parent,f=>{
    button(`${f.name} · ${f.category} · ${f.kind}: ${f.title}`,()=>qualityAct(()=>qualityResponsibility(f.identity)),parent);
  });
}
qualityEl('quality-category').oninput=()=>qualityAct(qualityFindings);
for(const control of document.querySelectorAll('.review-sections button'))if(control.id!=='test-quality')control.addEventListener('click',()=>{if(qualityState.active)qualityClose();},true);
const beforeQualityRender=render;
render=function(){
  if(qualityState.active)qualityClose();
  const result=beforeQualityRender();
  qualityApplicationScore();
  return result;
};
function qualityApplicationScore(load=true) {
  if(mode.kind!=='application')return;
  const parent=document.querySelector('#application-overview .entry-purpose');
  if(!parent)return;
  let link=parent.querySelector('.quality-overview-link');
  if(!link){link=button('Test Quality · optional AI assessment',()=>qualityOpen(),parent);link.className='quality-overview-link';}
  const show=()=>{
    const saved=qualityState.latest;
    link.textContent=saved?`Saved AI test-quality score · ${qualityScore(saved.summary?.score)} · ${saved.status}`:'Test Quality · optional AI assessment';
    link.title='Optional AI judgment on pinned evidence, not measured coverage. Saved evidence may differ from current source.';
  };
  show();
  if(load&&qualityState.loadedReport!==report) {
    const current=report;
    qualityRequest('/api/test-quality/status').then(data=>{
      if(report!==current)return;
      qualityState.loadedReport=current;qualityState.latest=data.assessments?.[0]||null;
      if(link.isConnected)show();
    }).catch(error=>{if(link.isConnected){link.textContent='Test Quality · saved results unavailable';link.title=error.message;}});
  }
}
