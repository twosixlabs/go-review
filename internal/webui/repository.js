'use strict';
let repositoryRun=null,repositoryReviewBusy=false;
let reviewPollTimer=null,reviewPollPending=false,reviewRequestPending=false;
let reviewProgress=null,reviewProgressError='',reviewConfirmedAt=0,reviewObservedAt=0;
const reviewControls=['review-repository','analyze-only','review-modules','run-tests','import-coverage','scope-load','scope-save'];

function setReviewBusy(busy) {
  repositoryReviewBusy=busy;
  for(const id of reviewControls)$(id).disabled=busy;
  $('review-repository').textContent=busy?'Review running…':'Review repository';
}

function reviewDuration(start) {
  const seconds=Math.max(0,Math.floor((Date.now()-start)/1000));
  return `${Math.floor(seconds/60)}m ${String(seconds%60).padStart(2,'0')}s`;
}

function renderReviewProgress() {
  if(!repositoryReviewBusy)return;
  const panel=$('review-progress');
  panel.hidden=false;
  panel.setAttribute('aria-busy','true');
  $('review-phase').textContent=reviewProgress?.phase||'Starting repository review';
  $('review-target').textContent=reviewProgress?.module||reviewProgress?.root||$('directory').value;
  const bar=$('review-progress').querySelector('progress');
  const total=reviewProgress?.totalStages,completed=reviewProgress?.completedStages;
  const measured=Number.isInteger(total)&&total>0&&Number.isInteger(completed)&&completed>=0&&completed<=total;
  if(measured) {
    bar.max=total;bar.value=completed;
    $('review-stage-count').textContent=`${completed} of ${total} stages complete · ${Math.round(completed/total*100)}%`;
  } else {
    bar.removeAttribute('value');
    $('review-stage-count').textContent='Determining review stages…';
  }
  bar.setAttribute('aria-label',measured?$('review-stage-count').textContent:'Review stage count not yet available');
  const command=reviewProgress?.command;
  $('review-command').hidden=!command;
  $('review-command').textContent=command?
    `${[command.executable,...command.args].map(arg=>/^[\w./:=@,+-]+$/.test(arg)?arg:JSON.stringify(arg)).join(' ')}\nWorking directory: ${command.directory}`:'';
  const start=Date.parse(reviewProgress?.startedAt);
  const phaseStart=Date.parse(reviewProgress?.phaseStartedAt);
  $('review-elapsed').textContent=`${Number.isFinite(start)?'Elapsed':'Connected for'} ${reviewDuration(Number.isFinite(start)?start:reviewObservedAt)}`+
    (Number.isFinite(phaseStart)?` · Stage ${reviewDuration(phaseStart)}`:'');
  $('review-connection').textContent=reviewProgressError?
    `Status connection unavailable: ${reviewProgressError}. Retrying; completion is not confirmed.`:
    reviewConfirmedAt?`Server confirmed running ${reviewDuration(reviewConfirmedAt)} ago. Waiting for this stage to finish.`:'Waiting for server confirmation…';
  $('review-connection').classList.toggle('error',!!reviewProgressError);
  status(`Review running · ${$('review-phase').textContent} · ${$('review-target').textContent}`);
}

function monitorReview(progress) {
  if(!repositoryReviewBusy)reviewObservedAt=Date.now();
  setReviewBusy(true);
  if(progress) {
    reviewProgress=progress;
    reviewConfirmedAt=Date.now();
    reviewProgressError='';
  }
  renderReviewProgress();
  watchReviews();
}

function watchReviews() {
  // Keep watching while idle: a different tab or CLI can start the next run.
  if(reviewPollTimer===null)reviewPollTimer=setInterval(()=>{
    renderReviewProgress();
    return pollReview();
  },1000);
}

function stopReviewMonitor() {
  setReviewBusy(false);
  $('review-progress').hidden=true;
  $('review-progress').setAttribute('aria-busy','false');
  reviewProgress=null;reviewProgressError='';reviewConfirmedAt=0;
}

function showReviewReport(next) {
  report=next;
  history=[];mode={kind:'application'};
  if(report)render();
  else {
    for(const id of ['graph','list','signal-summary','application-overview','detail'])$(id).replaceChildren();
    $('application-overview').hidden=true;
    $('breadcrumb').textContent='';
  }
}

let reviewStatusLoaded=false,reviewLastError='';
async function pollReview() {
  if(reviewPollPending)return;
  reviewPollPending=true;
  const controller=new AbortController();
  const timeout=setTimeout(()=>controller.abort(),8000);
  try {
    const response=await fetch('/api/review',{signal:controller.signal});
    const state=await response.json();
    if(!response.ok)throw Error(state.error||response.statusText);
    if(state.running)monitorReview(state.progress);
    else if(!reviewRequestPending) {
      const reconnecting=repositoryReviewBusy;
      if(reconnecting)stopReviewMonitor();
      if(state.run&&(reconnecting||repositoryRun?.directory!==state.run.directory||repositoryRun?.status!==state.run.status)) {
        if(reconnecting||reviewStatusLoaded) {
          const snapshot=await fetch('/api/snapshot');
          if(snapshot.ok) {
            showReviewReport(await snapshot.json());
          } else if(snapshot.status===404)showReviewReport(null);
          else throw Error(`load completed snapshot: ${snapshot.statusText}`);
        }
        showRepositoryRun(state.run);
        $('directory').value=state.run.root;
        if(!state.error)reviewRunStatus(state.run);
        if(reconnecting&&!report)repositoryRunDetail();
      } else if(reconnecting&&!state.error)status('Review stopped without a completed report. Check server diagnostics.',true);
      if(!state.run&&repositoryRun)showRepositoryRun(null);
      if(state.error&&(reconnecting||state.error!==reviewLastError))
        status('Repository review failed: '+state.error+'. Displayed results were not refreshed.',true);
      reviewLastError=state.error||'';
    }
    reviewStatusLoaded=true;
  } catch(error) {
    reviewProgressError=error.message;
    if(repositoryReviewBusy)renderReviewProgress();
    else status('Review status unavailable: '+error.message,true);
  } finally {
    clearTimeout(timeout);
    reviewPollPending=false;
  }
}

function showRepositoryRun(run) {
  repositoryRun=run;
  $('review-results').hidden=!run;
  const select=$('review-modules');
  select.replaceChildren();
  select.hidden=!run;
  if(!run)return;
  for(const module of run.modules) {
    const option=el('option',module.module||module.directory,select);
    option.value=module.directory;
    option.disabled=!!module.error;
    if(module.error)option.textContent+=' · '+module.error;
    option.selected=module.directory===report?.root;
  }
}

function reviewRunStatus(run) {
  const unavailable=run.modules.filter(m=>m.error).length;
  const failed=(run.steps||[]).filter(s=>s.status==='fail'||s.status==='error').length;
  const skipped=(run.steps||[]).filter(s=>s.status==='not-run').length;
  const parts=[run.status==='analyzed'?'Static analysis complete':run.status==='canceled'?'Review canceled':'Review complete',
    `${run.modules.length} module${run.modules.length===1?'':'s'}`];
  if(failed)parts.push(`${failed} failed check${failed===1?'':'s'}`);
  if(unavailable)parts.push(`${unavailable} module${unavailable===1?'':'s'} unavailable`);
  if(skipped)parts.push(`${skipped} check${skipped===1?'':'s'} not run`);
  if(failed||unavailable||skipped)parts.push('See Review results');
  status(parts.join(' · '),failed>0||unavailable>0||run.status==='canceled');
}

function repositoryRunDetail() {
  if(!repositoryRun)return;
  const target=$('detail');
  target.replaceChildren();
  el('h2','Repository review',target);
  el('p',repositoryRun.root,target);
  if(repositoryRun.finishedAt)el('p','Completed: '+new Date(repositoryRun.finishedAt).toLocaleString(),target);
  scopeDetailContent(target,repositoryRun.scope,repositoryRun.exclusions);
  for(const module of repositoryRun.modules) {
    if(module.error)el('p',module.directory+': '+module.error,target,'error');
    if(module.exclusions?.length) {
      el('h3',module.module||module.directory,target);
      for(const item of module.exclusions)el('p',`${item.path} — ${item.reason}`,target);
    }
  }
  for(const step of repositoryRun.steps||[]) {
    const row=el('details',undefined,target);
    row.dataset.reviewStep=step.id;
    row.open=step.id===mode.reviewStepID;
    el('summary',`${step.status} · ${step.title}${step.module?' · '+step.module:''}`,row);
    if(step.detail)el('pre',step.detail,row);
    for(const command of step.commands||[])
      el('pre',`${command.executable} ${command.args.join(' ')}\nexit ${command.exitCode}\n${command.stdout||''}\n${command.stderr||''}`,row);
  }
}

async function reviewRepository() {
  if(repositoryReviewBusy)return;
  reviewRequestPending=true;
  monitorReview();
  let rejected=false;
  try {
    const response=await fetch('/api/review',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({directory:$('directory').value,execute:true})});
    const body=await response.json();
    if(!response.ok) {
      rejected=true;
      throw Error(body.error||response.statusText);
    }
    showReviewReport(body.report);
    showRepositoryRun(body.run);
    $('directory').value=body.run.root;
    stopReviewMonitor();
    reviewRunStatus(body.run);
    if(!report)repositoryRunDetail();
  } catch(error) {
    // A disconnected POST does not establish that the server stopped working.
    // Reconcile with the server rather than silently abandoning its progress.
    if(rejected) {
      stopReviewMonitor();
      status('Repository review failed: '+error.message,true);
    } else {
      reviewProgressError=error.message;
      renderReviewProgress();
    }
  } finally {
    reviewRequestPending=false;
    if(repositoryReviewBusy)await pollReview();
  }
}

$('open').onsubmit=event=>{event.preventDefault();reviewRepository();};
$('analyze-only').onclick=async()=>{
  const loaded=await load('/api/analyze',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({directory:$('directory').value,documentationDirectory:$('docdir').value})});
  if(loaded)showRepositoryRun(null);
};
$('review-results').onclick=repositoryRunDetail;
$('review-modules').onchange=async()=>{
  $('review-modules').disabled=true;
  const loaded=await load('/api/review/select',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({directory:$('review-modules').value})});
  if(loaded&&repositoryRun){$('directory').value=repositoryRun.root;reviewRunStatus(repositoryRun);}
  $('review-modules').disabled=false;
};
document.addEventListener('DOMContentLoaded',()=>{watchReviews();pollReview();});

// Snapshot and review-status requests can finish in either order on reload.
// The picker must describe the rendered report, not default to the first module.
const beforeRepositoryRender=render;
render=function() {
  beforeRepositoryRender();
  if(report&&repositoryRun&&repositoryRun.modules.some(m=>m.directory===report.root))
    $('review-modules').value=report.root;
};

function scopeDetailContent(target,scope,exclusions) {
  el('h3','Applied review scope',target);
  if(!scope?.root)el('p','This retained snapshot predates saved review scopes. Save a scope and run a new review to apply it.',target);
  else {
    el('p',`Scope root: ${scope.root}`,target);
    el('p','Generated source excluded from review evidence, retained for build/type resolution. Repository verification is included by default in new scopes and runs only during an explicit review.',target);
    for(const pattern of scope.excludePaths||[])el('p','Path exclusion: '+pattern,target);
    for(const pattern of scope.excludeTests||[])el('p','Test-name exclusion: '+pattern,target);
    for(const selection of scope.unitSelections||[])el('p','Unit selection: '+selection.package+' :: '+selection.tests.join(', '),target);
    el('p','Repository verification: '+(scope.verifyRepository?'included':'disabled in saved scope'),target);
  }
  for(const item of exclusions||[])el('p',`${item.path} — ${item.reason}`,target);
}

$('scope-results').onclick=()=>{
  const target=$('detail');target.replaceChildren();
  if(!report){el('p','Analyze or review a module to inspect its applied scope.',target);return;}
  scopeDetailContent(target,report?.scope,report?.exclusions);
};

async function editReviewScope(save) {
  const target=$('scope-status');
  $('scope-save').disabled=true;$('scope-load').disabled=true;
  try {
    const directory=$('directory').value;
    const lines=id=>$(id).value.split('\n').map(s=>s.trim()).filter(Boolean);
    const unitSelections=save?lines('scope-unit-tests').map(line=>{
      const parts=line.split('::');
      if(parts.length!==2||!parts[0].trim()||!parts[1].trim())throw Error('Unit selections require import-path::TestName,TestName');
      return {package:parts[0].trim(),tests:parts[1].split(/[\s,]+/).filter(Boolean)};
    }):[];
    const response=await fetch(save?'/api/review/scope':'/api/review/scope?directory='+encodeURIComponent(directory),save?{
      method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({directory,scope:{excludePaths:lines('scope-paths'),excludeTests:lines('scope-tests'),unitSelections,verifyRepository:$('scope-verification').checked}})
    }:undefined);
    const body=await response.json();
    if(!response.ok)throw Error(body.error||response.statusText);
    $('scope-paths').value=(body.excludePaths||[]).join('\n');
    $('scope-tests').value=(body.excludeTests||[]).join('\n');
    $('scope-unit-tests').value=(body.unitSelections||[]).map(s=>s.package+'::'+s.tests.join(',')).join('\n');
    $('scope-verification').checked=!!body.verifyRepository;
    target.textContent=save?'Saved for the next review. Existing results are unchanged.':'Loaded saved scope for '+body.root;
    target.classList.remove('error');
  }catch(error){target.textContent='Review scope: '+error.message;target.classList.add('error');}
  finally{$('scope-save').disabled=repositoryReviewBusy;$('scope-load').disabled=repositoryReviewBusy;}
}
$('scope-load').onclick=()=>editReviewScope(false);
$('scope-save').onclick=()=>editReviewScope(true);
