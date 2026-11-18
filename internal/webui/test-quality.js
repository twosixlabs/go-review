'use strict';
const qualityState={active:false,id:null,overview:null,selected:null,group:null,source:null,serial:0,timer:null};
const qualityEl=id=>document.getElementById(id);
function qualityIndicator(context={}) {
  if(qualityState.indicators?.assessmentID!==qualityState.latest?.id)return {value:null};
  const module=qualityState.indicators?.modules?.find(m=>m.module===report.module&&m.sourceSHA256&&m.sourceSHA256===report.sourceSHA256);
  if(!module||context.symbolID||context.location)return {value:null};
  const summary=context.package?module.packages?.find(p=>p.package===context.package)?.summary:module.summary;
  return {value:summary?.score??null,partial:!!summary?.partial};
}
async function qualityOpenScope(context={}) {
  const module=qualityState.indicators?.assessmentID===qualityState.latest?.id?
    qualityState.indicators?.modules?.find(m=>m.module===report.module&&m.sourceSHA256&&m.sourceSHA256===report.sourceSHA256):null;
  const path=context.package||sym(context.symbolID)?.package;
  qualityState.id=qualityState.latest?.id||null;
  qualityState.group=null;qualityState.selected=null;qualityState.source=null;
  qualityEl('quality-search').value='';qualityEl('quality-filter').value='';
  if(module&&qualityState.indicators.assessmentID) {
    qualityState.group=path?{snapshotID:module.snapshotID,module:module.module,package:path}:null;
  }
  await qualityOpen();
}
async function qualityRefreshIndicators(assessment) {
  const key=assessment&&`${assessment.id}:${assessment.status||''}`;
  if(!key){qualityState.indicators=null;qualityState.indicatorKey=null;qualityUpdateGauges();return;}
  if(qualityState.indicatorKey===key)return;
  if(qualityState.indicatorPending?.key===key)return qualityState.indicatorPending.promise;
  const promise=(async()=>{
    const data=await qualityRequest('/api/test-quality/query',{assessmentID:assessment.id,operation:'indicators'});
    if(qualityState.latest?.id!==assessment.id||qualityState.latest?.status!==assessment.status)return;
    qualityState.indicators=data;qualityState.indicatorKey=key;
    qualityUpdateGauges();
  })();
  qualityState.indicatorPending={key,promise};
  try {await promise;} finally {if(qualityState.indicatorPending?.promise===promise)qualityState.indicatorPending=null;}
}
function qualityUpdateGauges() {
  if(!report)return;
  for(const parent of document.querySelectorAll('[data-quality-summary]')) {
    const {context,interactive,includeMetrics}=JSON.parse(parent.dataset.qualitySummary);
    signalSummary(parent,context,interactive,includeMetrics);
  }
  for(const group of document.querySelectorAll('[data-quality-node]')) {
    const {context,x,y}=JSON.parse(group.dataset.qualityNode);
    nodeSignalIndicators(group,context,x,y);
  }
  qualityApplicationScore(false);
}
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
const qualityScore=value=>value===null||value===undefined?'Not assessed':value.toFixed(1)+' / 100';
function qualityCounts(summary) {
  const c=summary?.counts||{};
  return `${c.scored||0} scored · ${c.insufficient||0} insufficient · ${c.unreviewed||0} unreviewed · ${c.excluded||0} excluded`;
}
function qualityRenderProgress(status,summary,phase) {
  const parent=qualityEl('quality-job-progress'),bar=qualityEl('quality-progress-bar'),label=qualityEl('quality-progress-label');
  parent.hidden=!status;
  const active=['running','queued'].includes(status);
  parent.setAttribute('aria-busy',String(active));
  const c=summary?.counts,processed=c?(c.scored||0)+(c.insufficient||0):0,total=c?processed+(c.unreviewed||0):0;
  const noun='tests';
  if(active&&phase&&phase!=='individual-judgments') {
    bar.hidden=false;bar.removeAttribute('value');bar.removeAttribute('max');
    label.textContent=`${status} · ${phase.replaceAll('-',' ')} · ${processed} of ${total} ${noun} individually processed`;
    return;
  }
  if(status==='queued'||!total) {
    bar.removeAttribute('value');bar.removeAttribute('max');
    bar.hidden=!active;
    label.textContent=status==='queued'?'Queued · waiting to start':
      active?'Preparing assessment · test total not yet available':'No assessable '+noun+'.';
  } else {
    bar.hidden=false;bar.max=total;bar.value=processed;
    label.textContent=`${status} · ${processed} of ${total} ${noun} processed`;
  }
}
function qualityContext() {
  return {assessmentID:qualityState.id,operation:qualityState.source?'source':qualityState.selected?'test':'overview',
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
      `${config.external?'External':'Local'} provider: ${config.model} · ${config.baseURL}`:'AI disabled. Starting retains deterministic facts and authorized diagnostic observations; semantic judgments remain unknown.';
    qualityState.external=!!config.external;
    qualityEl('quality-external').hidden=!config.external;qualityEl('quality-start').disabled=!!qualityState.starting;
    await qualityPoll();
    if(qualityState.id){await qualityPackages();if(!qualityState.selected&&!qualityState.source)await qualityFunctions();}
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
  qualityRefreshIndicators(qualityState.latest).catch(error=>qualityNotice(error.message,true));
  select.replaceChildren();
  for(const a of list) {
    const option=el('option',`${a.createdAt||a.id} · ${a.status} · ${a.model||''}`,select);
    option.value=a.id;
  }
  if(!qualityState.id&&list.length)qualityState.id=(list.find(a=>a.unit==='test')||list[0]).id;
  if(qualityState.id&&!list.some(a=>a.id===qualityState.id)) {
    const option=el('option','Pinned assessment · '+qualityState.id,select);option.value=qualityState.id;
  }
  select.value=qualityState.id||'';
  const active=list.filter(a=>['running','queued'].includes(a.status));
  qualityEl('test-quality').textContent=active.length?'Test Quality · '+active.length+' active':'Test Quality';
  const current=list.find(a=>a.id===qualityState.id);
  qualityRenderProgress(current?.status,current?.summary,current?.phase);
  qualityEl('quality-cancel').hidden=!current||!['running','queued'].includes(current.status);
  if(current)qualityEl('quality-progress').textContent=current.status+' · '+qualityCounts(current.summary);
  if(qualityState.active) {
    qualityHistoryRefresh();
    if(qualityState.id)await qualityOverview();
  }
  if(active.length)qualityState.timer=setTimeout(()=>qualityAct(qualityPoll),1200);
}
async function qualityOverview() {
  const id=qualityState.id,data=await qualityQuery('overview');
  if(id!==qualityState.id)return;
  qualityState.overview=data;
  qualityRenderProgress(data.status,data.summary,data.phase);
  const parent=qualityEl('quality-overview');
  const expanded=parent.dataset.assessment===id?[...parent.children].filter(child=>child.tagName==='SECTION'):[];
  const detailsOpen=parent.dataset.assessment===id&&!!parent.querySelector('.quality-assessment-details')?.open;
  parent.replaceChildren();parent.dataset.assessment=id;
  const metrics=el('div',undefined,parent,'quality-metrics');
  const gauge=(label,value)=>{
    const meter=el('div',undefined,metrics,'metric-gauge');
    const options=GoReviewGraph.gaugeOptions('coverage',value??null);
    meter.style.setProperty('--metric-color',options.gaugeColor);
    meter.setAttribute('role','meter');meter.setAttribute('aria-label',label);
    meter.setAttribute('aria-valuetext',qualityScore(value));
    if(value!==null&&value!==undefined){meter.setAttribute('aria-valuemin','0');meter.setAttribute('aria-valuemax','100');meter.setAttribute('aria-valuenow',value);}
    const dial=svg('svg',{viewBox:'-28 -28 56 34',class:'metric-dial','aria-hidden':'true'},meter);
    drawDial(dial,'signal',{value:value??null},options);
    const caption=el('span',undefined,meter,'metric-caption');
    el('strong',value===null||value===undefined?'Not assessed':value.toFixed(1),caption);
    el('span',label,caption,'metric-label');
  };
  gauge('Individual tests',data.summary?.score);
  for(const [name,value] of Object.entries(data.categories||{}))gauge(name.replaceAll('-',' '),value);
  const details=el('details',undefined,parent,'quality-assessment-details');
  details.open=detailsOpen;
  el('summary','Assessment details',details);
  el('p',`${data.analysis?.parsedSources||0} sources · ${data.analysis?.helpersAnalyzed||0} helpers · ${data.review?.requests||0} model requests`,details);
  if(data.diagnosticReason)el('p',data.diagnosticReason,details);
  button('Inspect collective behaviors and conditions',()=>qualityAct(async()=>{
    const target=el('section',undefined,parent),suite=await qualityQuery('suite');
    if(id!==qualityState.id)return;
    el('h3','Collective suite judgment · '+suite.status,target);if(suite.reason)el('p',suite.reason,target);
    await qualityPage('suite-groups',{},target,group=>{
      const section=el('section',undefined,target);el('h4',group.package,section);
      for(const rating of group.ratings||[]){
        el('p',`${rating.dimension} · ${rating.value}/4 · ${rating.rationale}`,section);
        qualityCitations(section,rating.evidence);
      }
      for(const finding of group.findings||[]){
        el('h5',finding.title,section);el('p',finding.rationale,section);
        if(finding.suggestion)el('p',finding.suggestion,section);
        qualityCitations(section,finding.evidence);
      }
    });
    await qualityPage('expectations',{},target,e=>{
      button(e.description+' · '+e.state,()=>qualityAct(async()=>{
        const detail=await qualityQuery('expectation',{expectationID:e.id});
        if(id!==qualityState.id)return;
        const section=el('section',undefined,target);el('h4',detail.description+' · '+detail.state,section);
        el('p',detail.basis+' · '+detail.rationale,section);qualityCitations(section,detail.evidence);
        if(detail.scope?.length)el('p',`Group observation from ${detail.scope.length} selected test${detail.scope.length===1?'':'s'}; other groups may contribute.`,section);
        for(const identity of detail.tests||[])button('Inspect contributing test',()=>qualityAct(()=>qualityResponsibility(identity)),section);
        await qualityPage('conditions',{expectationID:e.id},section,c=>{
          el('p',c.description+' · '+c.state+' · '+c.rationale,section);qualityCitations(section,c.evidence);
          for(const identity of c.tests||[])button('Inspect condition test',()=>qualityAct(()=>qualityResponsibility(identity)),section);
        });
      }),target);
    });
    await qualityPage('suite-findings',{},target,f=>{
      const section=el('section',undefined,target);el('h4',f.title,section);
      el('p',f.rationale,section);if(f.consequence)el('p',f.consequence,section);if(f.suggestion)el('p',f.suggestion,section);qualityCitations(section,f.evidence);
    });
  }),parent);
  button('Inspect fresh diagnostic observations',()=>qualityAct(async()=>{
    const target=el('section',undefined,parent);
    await qualityPage('diagnostics',{},target,record=>{
      const section=el('section',undefined,target);el('pre',JSON.stringify(record,null,2),section);
      const args={diagnosticIndex:record.diagnosticIndex};
      button('Inspect diagnostic runs',()=>qualityAct(async()=>{
        await qualityPage('diagnostic-runs',args,section,run=>{
          el('pre',JSON.stringify(run,null,2),section);
          button('Inspect attempts',()=>qualityAct(()=>qualityPage('diagnostic-attempts',{...args,runIndex:run.runIndex},section,attempt=>el('pre',JSON.stringify(attempt,null,2),section))),section);
        });
      }),section);
      button('Inspect diagnostic artifacts',()=>qualityAct(()=>qualityPage('diagnostic-artifacts',args,section,artifact=>el('pre',JSON.stringify(artifact,null,2),section))),section);
    });
  }),parent);
  parent.append(...expanded);
  qualityEl('quality-progress').textContent=`${data.status} · ${qualityCounts(data.summary)} · ${data.moduleCount||0} modules · ${data.unavailableModuleCount||0} unavailable`;
  const limits=qualityEl('quality-limitations');limits.replaceChildren();
  el('p',`${data.version} · ${data.model} · ${data.root||''} · assessment ${data.assessmentID}`,limits);
  el('p',`Evidence pinned ${data.createdAt||'with this assessment'}.`,limits);
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
  button('All project tests',
    ()=>qualityAct(()=>{qualityState.group=null;return qualityFunctions();}),parent);
  await qualityPage('modules',{},parent,g=>{
    const b=button(`${g.module} · ${g.package} · ${qualityScore(g.summary.score)} · ${g.findings} findings`,()=>qualityAct(async()=>{
      qualityState.group={snapshotID:g.snapshotID,module:g.module,package:g.package};await qualityFunctions();
    }),parent);b.title=qualityCounts(g.summary);
  });
}
async function qualityFunctions() {
  qualityState.serial++;
  const parent=qualityEl('quality-functions');parent.replaceChildren();
  qualityEl('quality-list-heading').textContent=qualityState.group?.package?'Tests · '+qualityState.group.package:'Project tests';
  qualityState.selected=null;qualityState.source=null;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();
  el('p','Select a test to review its assessment and evidence.',qualityEl('quality-detail'),'muted');
  await qualityPage('tests',{...qualityState.group,query:qualityEl('quality-search').value,status:qualityEl('quality-filter').value},parent,item=>{
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
  const data=await qualityQuery('test',input);
  if(id!==qualityState.id||serial!==qualityState.serial)return;
  qualityState.selected=input;qualityState.source=null;qualityEl('quality-source').replaceChildren();
  const parent=qualityEl('quality-detail');parent.replaceChildren();
  el('h3',data.name,parent);el('p',data.status+(data.reason?' · '+data.reason:''),parent);
  if(data.reusedFrom)el('p',`Reused from assessment ${data.reusedFrom.assessmentID} · ${data.reusedFrom.model} · retained project inputs unchanged.`,parent,'muted');
  if(data.declaration?.fileID)qualityCitations(parent,[data.declaration]);
  for(const r of data.ratings||[]) {
    el('h4',`${r.dimension.replaceAll('-',' ')} · ${r.state==='assessed'?r.value+' / 4':r.state}`,parent);
    el('p',r.rationale,parent);qualityCitations(parent,r.evidence);
  }
  for(const f of data.findings||[]) {
    const section=el('section',undefined,parent);el('h4',f.title,section);el('small',f.category+' · AI '+f.kind,section);
    el('p',f.rationale,section);if(f.consequence)el('p','Consequence: '+f.consequence,section);if(f.suggestion)el('p','Suggested test/improvement: '+f.suggestion,section);qualityCitations(section,f.evidence);
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
  if(data.testKind) {
    button('Deterministic checking sites, cases and reliability observations',()=>qualityAct(async()=>{
      const target=el('section',undefined,details);
      await qualityPage('facts',input,target,fact=>{
        el('pre',JSON.stringify(fact,null,2),target);
        for(const check of fact.checks||[])qualityCitations(target,[check.evidence]);
        for(const c of fact.cases||[])qualityCitations(target,[c.evidence]);
      });
    }),details);
    button('Test outcomes & run scope',()=>qualityAct(()=>qualityEvidence(input.snapshotID,'test-runs',
      {testName:data.name,package:data.package,limit:20},details)),details);
    button('Browse supporting production declarations',()=>qualityAct(async()=>{
      const target=el('section',undefined,details);
      const load=async(offset=0)=>{
        const symbols=await qualityQuery('evidence',{snapshotID:input.snapshotID,evidenceOperation:'symbols',args:{offset,limit:20}});
        for(const s of symbols.items||[])button(s.package+' · '+s.name,()=>{
          const context=el('section',undefined,target);el('h4','Supporting production: '+s.name,context);
          if(s.location?.fileID)button('Read production source',()=>qualityAct(()=>qualitySource({
            snapshotID:input.snapshotID,fileID:s.location.fileID,startLine:s.location.line,endLine:Math.min(s.location.endLine,s.location.line+99)})),context);
          for(const [label,operation,args] of [
            ['Statement coverage (not test quality)','coverage',{symbolID:s.id,limit:100}],
            ['Control-flow alternatives','evidence',{symbolID:s.id,section:'controlFlowNodes'}],
            ['Data flow & opaque boundaries','dataflow',{symbolID:s.id}]
          ])button(label,()=>qualityAct(()=>qualityEvidence(input.snapshotID,operation,args,context)),context);
        },target);
        if(symbols.nextOffset!==null&&symbols.nextOffset!==undefined)button('More production declarations',()=>qualityAct(()=>load(symbols.nextOffset)),target);
      };
      await load();
    }),details);
    return;
  }
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
  const pages=[data,data.results,data.blocks,data.unitEvidence?.runs,data.observations].filter(p=>Array.isArray(p?.items));
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
  if(input.operation==='test')await qualityResponsibility(input);
  else if(input.operation==='source')await qualitySource(input);
  else {qualityState.selected=null;qualityState.source=null;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();}
}
qualityEl('test-quality').onclick=qualityOpen;
qualityEl('quality-return').onclick=qualityClose;
qualityEl('quality-start').onclick=()=>qualityAct(async()=>{
  if(qualityState.external&&!qualityEl('quality-consent').checked)throw Error('Approve the external-provider disclosure before starting.');
  if(qualityState.starting)return;
  qualityState.starting=true;qualityEl('quality-start').disabled=true;
  qualityNotice('Starting assessment…');
  try {
    const a=await qualityRequest('/api/test-quality/start',{confirmExternal:qualityState.external&&qualityEl('quality-consent').checked,allowExecution:qualityEl('quality-execute').checked});
    qualityState.id=a.id;qualityState.selected=null;qualityState.source=null;qualityState.group=null;qualityState.serial++;
    qualityEl('quality-consent').checked=false;qualityEl('quality-detail').replaceChildren();qualityEl('quality-source').replaceChildren();
    qualityNotice('Assessment started in the background. Continue browsing or chatting; only Cancel stops it.');
    await qualityPoll();await qualityPackages();await qualityFunctions();
  } finally {
    qualityState.starting=false;qualityEl('quality-start').disabled=false;
  }
});
qualityEl('quality-cancel').onclick=()=>qualityAct(async()=>{await qualityRequest('/api/test-quality/cancel',{assessmentID:qualityState.id});await qualityPoll();});
qualityEl('quality-refresh').onclick=()=>qualityAct(async()=>{await qualityPoll();if(qualityState.id){await qualityPackages();await qualityFunctions();}});
qualityEl('quality-assessments').onchange=()=>qualityAct(async()=>{
  qualityState.id=qualityEl('quality-assessments').value;qualityState.group=null;qualityState.serial++;
  qualityHistoryRefresh();
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
  if(load&&report&&qualityState.loadedReport!==report&&qualityState.loadingReport!==report) {
    const current=report;
    qualityState.loadingReport=current;
    qualityRequest('/api/test-quality/status').then(async data=>{
      if(report!==current)return;
      qualityState.loadedReport=current;qualityState.latest=data.assessments?.[0]||null;
      await qualityRefreshIndicators(qualityState.latest);
      qualityApplicationScore(false);
    }).catch(error=>{
      qualityNotice(error.message,true);
      const link=document.querySelector('.quality-overview-link');
      if(link){link.textContent='Test Quality · saved results unavailable';link.title=error.message;}
    }).finally(()=>{if(qualityState.loadingReport===current)qualityState.loadingReport=null;});
  }
  if(mode.kind!=='application')return;
  const parent=document.querySelector('#application-overview .entry-purpose');
  if(!parent)return;
  let link=parent.querySelector('.quality-overview-link');
  if(!link){link=button('Test Quality · optional AI assessment',()=>qualityOpen(),parent);link.className='quality-overview-link';}
  const show=()=>{
    const saved=qualityState.latest,quality=qualityIndicator();
    link.textContent=saved?`Test Quality · ${quality.value===null?'Not assessed':quality.value.toFixed(1)+' / 100'}`:'Test Quality · Not assessed';
    link.title='Open Test Quality results for this source snapshot.';
  };
  show();
}
