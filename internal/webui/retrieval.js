'use strict';
const retrievalState={report:null,snapshot:null,data:null,sequence:0,statusSequence:0,controller:null,timer:null,last:null};

function retrievalControls() {
  const data=retrievalState.data,ready=data?.state==='ready';
  const running=['discovering','indexing','preparing'].includes(data?.state);
  $('retrieval-prepare').disabled=!data?.enabled||running;
  $('retrieval-cancel').hidden=!running;
  $('retrieval-external').hidden=!data?.external;
  $('retrieval-search').disabled=!ready||!$('retrieval-query').value.trim();
  $('retrieval-code').disabled=!ready||!report?.symbols?.some(s=>s.id===mode.id&&['function','method','closure'].includes(s.kind));
  const progress=$('retrieval-progress');
  progress.hidden=!running;
  if(running&&data.status?.total>0){progress.max=data.status.total;progress.value=data.status.completed;}
  else progress.removeAttribute('value');
}

function retrievalFailure(error) {
  $('retrieval-status').textContent=error.message;
  $('retrieval-status').classList.add('error');
}

async function retrievalLoadStatus() {
  clearTimeout(retrievalState.timer);
  const current=report,sequence=++retrievalState.statusSequence;
  try{
    const response=await fetch('/api/retrieval/status');
    const data=await response.json();
    if(current!==report||sequence!==retrievalState.statusSequence)return;
    if(!response.ok)throw Error(data.error||'Embedding status HTTP '+response.status);
    retrievalState.data=data;retrievalState.snapshot=data.snapshot?.id;
    $('retrieval-status').classList.toggle('error',Boolean(data.error));
    const status=data.status;
    const trials=status?.profile?.trials||[],trialResults=$('retrieval-trial-results');
    $('retrieval-trials').hidden=!trials.length;
    trialResults.replaceChildren();
    for(const trial of trials)
      el('li',`${trial.direction}, ${trial.representation}, ${trial.case}: target rank ${trial.rank}/${trial.candidates}; ${trial.ties} tied (including target).`,trialResults);
    $('retrieval-status').textContent=!data.enabled
      ?'Configure embeddings.json and restart go-review to enable optional semantic search. Literal search remains available.'
      :data.error||`${data.model||'Configured model'}: ${data.state}. `+
        (status?.total?`${status.completed}/${status.total} chunks; ${status.reused} reused. `:'')+
        (status?.profile?.dimensions?`${status.profile.dimensions} dimensions. `:'')+
        'Code/text capability is unverified; results are best-effort candidates, not quality judgments.';
    retrievalControls();
    if(['discovering','indexing','preparing'].includes(data.state))
      retrievalState.timer=setTimeout(retrievalLoadStatus,750);
  }catch(error){if(current===report&&sequence===retrievalState.statusSequence)retrievalFailure(error);}
}

function retrievalSync() {
  if(!report)return;
  retrievalControls();
  if(retrievalState.report===report)return;
  retrievalState.report=report;retrievalState.snapshot=null;retrievalState.data=null;retrievalState.last=null;
  retrievalState.sequence++;retrievalState.controller?.abort();
  $('retrieval-results').replaceChildren();$('retrieval-more').hidden=true;
  retrievalLoadStatus();
}

async function retrievalAction(action) {
  const snapshotID=retrievalState.snapshot,current=report;
  try{
    const response=await fetch('/api/retrieval/index',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({snapshotID,action,confirmExternal:$('retrieval-consent').checked})});
    if(!response.ok){const data=await response.json();throw Error(data.error||'Index operation HTTP '+response.status);}
    if(current===report)await retrievalLoadStatus();
  }catch(error){if(current===report)retrievalFailure(error);}
}

function retrievalOpen(chunk) {
  if(retrievalState.report!==report)return;
  if(chunk.documentID){
    const doc=[...(report.documents||[]),...(report.entryPoints?.sources||[])].find(d=>d.id===chunk.documentID);
    if(!doc){retrievalFailure(Error('Retained document is no longer available.'));return;}
    openDocumentation(doc);return;
  }
  const symbol=report.symbols?.find(s=>s.id===chunk.entityID);
  if(symbol&&['function','method','closure'].includes(symbol.kind)){
    go({kind:'logical',id:symbol.id,inspectorOpen:true});return;
  }
  const file=[...(report.files||[]),...(report.testSources||[]).map(t=>t.file)].find(f=>f.id===chunk.fileID);
  if(!file||(file.sha256&&file.sha256!==chunk.sourceSHA256)){
    retrievalFailure(Error('Retained source identity does not match the candidate.'));return;
  }
  mode={...mode,inspectorOpen:true};document.body.classList.add('review-inspector-open');
  detail(chunk.name||chunk.path,'Original retained source; retrieval similarity is not verified behavior.');
  const text=file.source.split('\n').slice(chunk.startLine-1,chunk.endLine).join('\n');
  el('pre',text,$('detail'));
}

async function retrievalSearch(entityID='',offset=0) {
  const snapshotID=retrievalState.snapshot,current=report;
  if(!snapshotID||retrievalState.data?.state!=='ready'){retrievalFailure(Error('Prepare this snapshot index first.'));return;}
  retrievalState.controller?.abort();
  const controller=new AbortController(),sequence=++retrievalState.sequence;
  retrievalState.controller=controller;
  const input=offset&&retrievalState.last?{...retrievalState.last,offset}
    :{snapshotID,query:entityID?'':$('retrieval-query').value.trim(),entityID,offset,limit:10};
  try{
    const response=await fetch('/api/retrieval/query',{method:'POST',headers:{'Content-Type':'application/json'},signal:controller.signal,body:JSON.stringify(input)});
    const result=await response.json();
    if(sequence!==retrievalState.sequence||current!==report||snapshotID!==retrievalState.snapshot)return;
    if(!response.ok)throw Error(result.error||'Retrieval HTTP '+response.status);
    if(result.snapshotID!==snapshotID)throw Error('Retrieval returned a different snapshot.');
    retrievalState.last=input;
    const results=$('retrieval-results');results.replaceChildren();
    for(const limitation of result.limitations||[])el('p',limitation,results,'muted');
    for(const hit of result.hits||[]){
      const row=el('div',undefined,results);
      button(`${hit.chunk.name||hit.chunk.path} · ${hit.chunk.path||''}:${hit.chunk.startLine}`,
        ()=>retrievalOpen(hit.chunk),row);
      el('span',(hit.reasons||[]).join(' · '),row,'muted');
    }
    if(!result.hits?.length)el('p','No candidates returned. This does not establish absence of relevant code or tests.',results);
    $('retrieval-more').hidden=result.nextOffset==null;
    $('retrieval-more').onclick=()=>retrievalSearch('',result.nextOffset);
  }catch(error){
    if(sequence===retrievalState.sequence&&current===report){
      if(error.name==='AbortError')$('retrieval-status').textContent='Retrieval query canceled.';
      else retrievalFailure(error);
    }
  }
}

$('retrieval-prepare').onclick=()=>retrievalAction('start');
$('retrieval-cancel').onclick=()=>retrievalAction('cancel');
$('retrieval-form').onsubmit=event=>{event.preventDefault();retrievalSearch();};
$('retrieval-code').onclick=()=>retrievalSearch(mode.id);
$('retrieval-query').oninput=()=>{
  retrievalState.sequence++;retrievalState.controller?.abort();retrievalState.last=null;
  $('retrieval-results').replaceChildren();$('retrieval-more').hidden=true;retrievalControls();
};
$('retrieval-panel').addEventListener('toggle',()=>{if($('retrieval-panel').open&&report)retrievalLoadStatus();});
const retrievalPreviousRender=render;
render=()=>{retrievalPreviousRender();retrievalSync();};
retrievalSync();
