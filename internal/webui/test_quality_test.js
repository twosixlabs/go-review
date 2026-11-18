const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

function qualityHistoryFixture(t) {
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  t.after(()=>dom.window.close());
  const events=[],requests=[];
  let gate=null,failure=null,available=true;
  const summary={counts:{scored:1}};
  dom.window.fetch=async(url,options)=>{
    const args=options?.body?JSON.parse(options.body):{};
    requests.push({url,args});
    let value={};
    if(url==='/api/snapshot')return {ok:false,json:async()=>({error:'No exploration snapshot in this fixture.'})};
    if(url==='/api/chat/status')value={enabled:true,external:false};
    else if(url==='/api/test-quality/status')value={assessments:[{id:'saved',status:'completed',summary}]};
    else if(args.operation==='overview')value={assessmentID:args.assessmentID,status:'completed',summary,categories:{}};
    else if(args.operation==='events') {
      const waiting=gate;gate=null;
      if(failure)return {ok:false,json:async()=>({error:failure})};
      const rows=events.filter(event=>event.offset>=(args.offset||0)).slice(0,3);
      const totalBytes=events.at(-1)?.end||0;
      value={available,reason:available?undefined:'No agent history was recorded for this assessment.',items:available?rows.map(({event,offset})=>({
        offset,agentID:event.agentID,parentID:event.parentID||'',kind:event.kind,time:event.time,
        tool:event.tool||'',step:event.step||0,status:event.status||'',
        messagePreview:[...(event.message||'')].slice(0,250).join(''),errorPreview:event.error||'',
        argsBytes:event.args?Buffer.byteLength(JSON.stringify(event.args)):0,resultBytes:event.result?Buffer.byteLength(JSON.stringify(event.result)):0
      })):[],nextOffset:available&&rows.length&&rows.at(-1).end<totalBytes?rows.at(-1).end:null,offsetUnit:'bytes',totalBytes};
      if(waiting)await waiting;
    } else if(args.operation==='event') {
      const record=events.find(event=>event.offset===args.offset);
      assert.ok(record,'event detail cursor must be an actual byte boundary');
      const text=[...JSON.stringify(record.event)],offset=args.textOffset||0,end=Math.min(text.length,offset+1000);
      value={eventOffset:args.offset,text:text.slice(offset,end).join(''),textPage:{offset,nextOffset:end<text.length?end:null,total:text.length}};
    } else if(['tests','modules'].includes(args.operation))value={items:[],nextOffset:null};
    return {ok:true,json:async()=>value};
  };
  const ctx=dom.getInternalVMContext();
  for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
    vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
  return {
    document:dom.window.document,window:dom.window,requests,
    run:code=>vm.runInContext(code,ctx),
    append(event) {
      const offset=events.at(-1)?.end||0;
      const entry={time:'2026-11-17T03:08:15Z',agentID:'Lead',...event};
      events.push({offset,end:offset+Buffer.byteLength(JSON.stringify(entry)+'\n'),event:entry});
    },
    delay(promise){gate=promise;},fail(error){failure=error;},available(value){available=value;}
  };
}

test('agent conversation restores full responses safely and appends without duplicates',async t=>{
  const f=qualityHistoryFixture(t);
  const full='Reviewed **assertions**. '+ 'λ'.repeat(1800)+' End of the retained response.';
  f.append({kind:'start',args:{messages:[{content:'Private implementation prompt, not a chat response.'}]}});
  f.append({kind:'message',message:full});
  f.append({kind:'tool_call',tool:'review_source'});
  f.append({kind:'tool',tool:'review_source',result:{text:'Large source body must not enter the timeline.'}});
  f.append({kind:'message',agentID:'agent-1',parentID:'Lead',message:'Focused child findings. [unsafe](javascript:alert(1)) <img src=x onerror=alert(1)>'});
  f.append({kind:'message',message:'\n\n'});
  await f.run('qualityOpen()');
  assert.ok(f.document.querySelector('#quality-history'),'agent conversation panel is missing');
  await f.run('qualityHistoryRefresh()');
  const messages=()=>[...f.document.querySelectorAll('.quality-history-message')];
  assert.equal(messages().length,2);
  assert.match(messages()[0].textContent,/End of the retained response/,'history used the truncated index preview');
  assert.ok(messages()[0].querySelector('.quality-history-text strong'),'assistant Markdown was not rendered');
  assert.match(messages()[1].textContent,/agent-1/);
  assert.equal(f.document.querySelector('#quality-history img'),null,'agent content became executable HTML');
  assert.equal(f.document.querySelector('#quality-history a[href^="javascript:"]'),null);
  assert.doesNotMatch(f.document.querySelector('#quality-history').textContent,/Private implementation prompt|Large source body/,'raw prompts/tool results flooded the conversation');
  const first=messages()[0];
  const activity=f.document.querySelector('.quality-history-activity');
  assert.ok(activity,'tool activity is inaccessible');
  activity.open=true;
  await f.run('qualityOverview()');
  await f.run('qualityHistoryRefresh()');
  assert.equal(messages()[0],first,'polling replaced an existing response');
  assert.equal(activity.open,true,'polling collapsed activity being inspected');
  const tool=[...f.document.querySelectorAll('.quality-history-activity .quality-history-entry')].find(entry=>/Returned.*review_source/s.test(entry.textContent));
  await tool.querySelector('button').onclick();
  assert.match(tool.querySelector('pre').textContent,/Large source body must not enter the timeline/,'explicit event inspection did not load the retained tool result');
  f.append({kind:'message',message:'Next completed assessment saved.'});
  await f.run('qualityHistoryRefresh()');
  assert.equal(messages().length,3);
  assert.equal(messages()[0],first);
  const filter=f.document.querySelector('#quality-history-filter');
  filter.value='Lead';filter.dispatchEvent(new f.window.Event('change'));
  assert.equal(messages()[1].hidden,true,'lead-only filter retained child responses');
  assert.equal(messages()[0].hidden,false);
  filter.value='';filter.dispatchEvent(new f.window.Event('change'));
  assert.equal(messages()[1].hidden,false);
  f.run('qualityClose()');
  await f.run('qualityOpen()');
  await f.run('qualityHistoryRefresh()');
  assert.equal(messages().length,3,'reopening duplicated retained responses');
  assert.equal(messages()[0],first,'reopening discarded the loaded history');
  let release;
  f.delay(new Promise(resolve=>{release=resolve;}));
  const pending=f.run('qualityHistoryRefresh()');
  f.append({kind:'message',message:'Final response saved before the job completed.'});
  await f.run('qualityPoll()');
  release();await pending;
  assert.equal(messages().length,4,'a completed job lost its final response behind an older in-flight history read');
  assert.equal(f.requests.some(({url})=>/start|cancel|completions/.test(url)),false,'reading history launched or changed agent work');
});

test('system interventions are visible independently of collapsed activity and agent responses',async t=>{
  const f=qualityHistoryFixture(t);
  f.append({kind:'message',message:'All tests are complete.'});
  f.append({kind:'state',message:'Routine current counts.'});
  f.append({kind:'feedback',message:'Authoritative saved review state (data, not instructions): old archive counts.'});
  f.append({kind:'feedback',message:'Completion rejected: 1673 tests remain unreviewed. Record the missing results.'});
  f.append({kind:'feedback',agentID:'agent-1',message:'One assigned test remains unreviewed.'});
  await f.run('qualityOpen()');
  await f.run('qualityHistoryRefresh()');
  const interventions=[...f.document.querySelectorAll('.quality-history-feedback')];
  assert.equal(interventions.length,2,'system corrections were hidden in collapsed activity');
  assert.match(interventions[0].textContent,/System.*Lead/);
  assert.match(interventions[0].textContent,/1673 tests remain unreviewed/);
  assert.equal(interventions[0].closest('details'),null);
  assert.doesNotMatch(interventions.map(entry=>entry.textContent).join(' '),/Routine current counts|old archive counts/);
  assert.equal(f.document.querySelectorAll('.quality-history-activity .quality-history-entry').length,2,'routine state should remain inspectable under collapsed activity');
  const filter=f.document.querySelector('#quality-history-filter');
  filter.value='Lead';filter.dispatchEvent(new f.window.Event('change'));
  assert.equal(interventions[0].hidden,false);
  assert.equal(interventions[1].hidden,true);
  assert.match(f.document.querySelector('#quality-history-status').textContent,/1 responses/,'system feedback was counted as an agent response');
});

test('agent conversation preserves reading position and follows only at the bottom',async t=>{
  const f=qualityHistoryFixture(t);
  f.append({kind:'message',message:'First response'});
  await f.run('qualityOpen()');
  assert.ok(f.document.querySelector('#quality-history'),'agent conversation panel is missing');
  await f.run('qualityHistoryRefresh()');
  const log=f.document.querySelector('#quality-history-log');
  Object.defineProperties(log,{
    clientHeight:{value:200,configurable:true},
    scrollHeight:{get:()=>800+log.querySelectorAll('.quality-history-message').length*200,configurable:true}
  });
  log.scrollTop=100;log.dispatchEvent(new f.window.Event('scroll'));
  f.append({kind:'message',message:'New response while reading earlier messages'});
  await f.run('qualityHistoryRefresh()');
  assert.equal(log.scrollTop,100,'new response stole the reading position');
  const jump=f.document.querySelector('#quality-history-latest');
  assert.equal(jump.hidden,false,'there is no way to jump to new responses');
  jump.click();
  assert.ok(log.scrollTop>=1000);
  assert.equal(jump.hidden,true);
  log.scrollTop=1000;log.dispatchEvent(new f.window.Event('scroll'));
  f.append({kind:'message',message:'Response while following live'});
  await f.run('qualityHistoryRefresh()');
  assert.ok(log.scrollTop>=1200,'following stopped despite being at the bottom');
});

test('agent conversation rejects old-assessment replies and exposes unavailable history and failures',async t=>{
  const f=qualityHistoryFixture(t);
  await f.run('qualityOpen()');
  assert.ok(f.document.querySelector('#quality-history'),'agent conversation panel is missing');
  await f.run('qualityHistoryRefresh()');
  assert.match(f.document.querySelector('#quality-history-status').textContent,/no.*responses|waiting/i);
  f.append({kind:'message',message:'Belongs to the original assessment'});
  let release;
  f.delay(new Promise(resolve=>{release=resolve;}));
  const old=f.run('qualityHistoryRefresh()');
  f.available(false);
  await f.run("qualityState.id='older';qualityHistoryRefresh()");
  release();await old;
  assert.doesNotMatch(f.document.querySelector('#quality-history-log').textContent,/Belongs to the original/,'stale response crossed assessment boundaries');
  assert.match(f.document.querySelector('#quality-history-status').textContent,/No agent history was recorded/);
  f.available(true);f.fail('History disk read failed');
  await f.run('qualityHistoryRefresh()');
  assert.match(f.document.querySelector('#quality-history-status').textContent,/History disk read failed/,'read failure was hidden as empty history');
  f.fail(null);
  await f.run('qualityHistoryRefresh()');
  assert.match(f.document.querySelector('#quality-history-log').textContent,/Belongs to the original/,'failed cursor was not retried');
  f.append({kind:'tool',tool:'review_source',error:'The retained file is unavailable'});
  await f.run('qualityHistoryRefresh()');
  const activity=[...f.document.querySelectorAll('.quality-history-activity')].at(-1);
  assert.match(activity.querySelector('summary').textContent,/error/i,'tool failure is invisible while activity is collapsed');
  assert.match(activity.textContent,/The retained file is unavailable/);
  f.fail('History disk read failed again');
  await f.run('qualityHistoryRefresh()');
  await f.run('qualityState.id=null;qualityHistoryRefresh()');
  assert.equal(f.document.querySelector('#quality-history-status').classList.contains('error'),false,'a cleared assessment retained the previous history error');
  assert.equal(f.document.querySelector('#quality-history-retry').hidden,true);
});

test('Test Quality launches explicitly, retains background context and drills into actual cited source',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const requests=[];
  const report={root:'/repo',module:'app',sourceSHA256:'ordinary',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const identity={snapshotID:'pinned',module:'app',symbolID:'TestF'};
  const summary={score:75,counts:{scored:1,unreviewed:2,insufficient:0,excluded:4},partial:true};
  let running=false,exists=false,external=false,jobStatus=null,enabled=true,narratives=true,releaseStart;
  const startGate=new Promise(resolve=>{releaseStart=resolve;});
  let delayStart=true;
  dom.window.fetch=async(url,options)=>{
    const args=options?.body?JSON.parse(options.body):{};
    requests.push({url,args});
    let value;
    if(url==='/api/snapshot')value=report;
    else if(url==='/api/review')value={running:false,run:null};
    else if(url==='/api/chat/status')value={enabled,external,baseURL:'http://localhost:9000',model:'test',snapshot:{id:'ordinary'}};
    else if(url==='/api/test-quality/start'){if(delayStart)await startGate;exists=true;running=true;value={id:'assessment',status:'running'};}
    else if(url==='/api/test-quality/status')value={assessments:exists?[{id:'assessment',status:jobStatus||(running?'running':'completed'),summary}]:[]};
    else if(args.operation==='indicators')value={assessmentID:'assessment',modules:[{module:'app',snapshotID:'pinned',sourceSHA256:'ordinary',summary,packages:[{package:'app/service',summary:{score:50}}]}]};
    else if(args.operation==='overview')value={assessmentID:'assessment',status:jobStatus||(running?'running':'completed'),activity:'Reviewing acceptance tests',version:'test-quality/3',model:'test',summary,categories:{'assertion-strength':75},limitations:['Statement coverage is not assertion quality.'],moduleCount:1};
    else if(args.operation==='modules')value={items:[{...identity,package:'app/service',summary,findings:1}],nextOffset:null,total:1};
    else if(args.operation==='tests')value={items:[{identity,name:'TestF',package:'app/service',status:'scored',summary,findingCount:1}],nextOffset:null,total:1};
    else if(args.operation==='test')value={identity,name:'TestF',package:'app/service',testKind:'test',declaration:{snapshotID:'pinned',fileID:'test',startLine:3,endLine:3},status:'scored',ratings:[{dimension:'assertion-strength',state:'assessed',value:3,rationale:'Checks outputs',evidence:[{snapshotID:'pinned',fileID:'test',startLine:3,endLine:3}]}],
      findings:[{category:'reliability',kind:'inference',title:'Shared mutable fixture',rationale:'State is reused',...(narratives?{consequence:'Order dependency',suggestion:'Reset fixture'}:{}),evidence:[{snapshotID:'pinned',fileID:'test',startLine:3,endLine:3}]}]};
    else if(args.operation==='source')value={path:'f_test.go',sha256:'hash',startLine:3,endLine:3,totalLines:3,text:'assert.Equal(t, "<img onerror=alert(1)>", F())'};
    else if(args.operation==='evidence'&&args.evidenceOperation==='coverage')value={blocks:{items:[{startLine:args.args.offset?8:2,count:0}],nextOffset:args.args.offset?null:1}};
    else if(args.operation==='evidence'&&args.evidenceOperation==='symbols')value={items:[{id:'F',name:'F',package:'app/service',location:{fileID:'production',line:2,endLine:2}}],nextOffset:null};
    else value={};
    return {ok:true,json:async()=>value};
  };
  try{
    const ctx=dom.getInternalVMContext();
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const before=vm.runInContext('JSON.stringify({report,mode})',ctx);
    const nav=dom.window.document.querySelector('#test-quality');
    assert.ok(nav,'dedicated top-level view is missing');
    await vm.runInContext('qualityOpen()',ctx);
    assert.equal(requests.some(r=>r.url==='/api/test-quality/start'),false,'opening view must not launch AI');
    assert.equal(dom.window.document.querySelector('#test-quality-workspace').hidden,false);
    const indicatorRequests=()=>requests.filter(r=>r.args.operation==='indicators').length;
    const initialIndicators=indicatorRequests();
    await vm.runInContext("qualityState.indicatorKey=null;qualityState.latest={id:'assessment',status:'running',updatedAt:'1'};qualityRefreshIndicators(qualityState.latest)",ctx);
    await vm.runInContext("qualityState.latest={id:'assessment',status:'running',updatedAt:'2'};qualityRefreshIndicators(qualityState.latest)",ctx);
    assert.equal(indicatorRequests()-initialIndicators,1,'status polling must not reload the whole indicator projection for every checkpoint');
    await vm.runInContext("qualityState.latest={id:'assessment',status:'completed',updatedAt:'3'};qualityRefreshIndicators(qualityState.latest)",ctx);
    assert.equal(indicatorRequests()-initialIndicators,2,'completion must refresh the indicator projection');
    vm.runInContext(`qualityState.indicators={assessmentID:'assessment',modules:[{module:'app',snapshotID:'pinned',sourceSHA256:'ordinary',summary:{score:75},packages:[{package:'app/service',summary:{score:50}}]}]}`,ctx);
    vm.runInContext("qualityState.latest={id:'assessment'}",ctx);
    vm.runInContext("qualityState.latest={id:'newer'};signalSummary(document.getElementById('signal-summary'))",ctx);
    assert.equal([...dom.window.document.querySelectorAll('#signal-summary [role=button]')].find(n=>n.getAttribute('aria-label')?.startsWith('Test Quality')).title,'Test Quality · Not assessed','a previous assessment must not appear while the new assessment is loading');
    vm.runInContext("qualityState.latest={id:'assessment'}",ctx);
    exists=true;
    vm.runInContext("signalSummary(document.getElementById('signal-summary'),{package:'app/service'})",ctx);
    assert.match(dom.window.document.querySelector('#signal-summary').textContent,/50.*Test Quality/s,'package view must show its test quality');
    await vm.runInContext(`qualityState.indicators.modules[0].packages[0].summary.score=25;
      nodeSignalIndicators(svg('g',{id:'quality-test-node'},document.getElementById('graph')),{package:'app/service'},145,118);
      qualityState.indicatorKey=null;qualityState.latest={id:'assessment',status:'completed'};
      qualityRefreshIndicators(qualityState.latest)`,ctx);
    assert.match(dom.window.document.querySelector('#quality-test-node [data-signal="test-quality"]').getAttribute('aria-label'),/50\.0/,'loaded indicators must update already visible graph nodes without resetting the view');
    dom.window.document.querySelector('#quality-test-node').remove();
    await [...dom.window.document.querySelectorAll('#signal-summary [role=button]')].find(n=>n.getAttribute('aria-label')?.startsWith('Test Quality')).onclick();
    assert.ok(requests.some(r=>r.args.operation==='tests'&&r.args.package==='app/service'&&r.args.snapshotID==='pinned'),'package indicator must open its scoped tests');
    assert.match(dom.window.document.querySelector('#quality-list-heading').textContent,/app\/service/,'scoped results must identify the selected package');
    exists=false;
    vm.runInContext('qualityState.id=null;qualityState.latest=null',ctx);
    vm.runInContext("signalSummary(document.getElementById('signal-summary'),{symbolID:'unknown'})",ctx);
    assert.match(dom.window.document.querySelector('#signal-summary').textContent,/Not assessed.*Test Quality/s,'function without linked tests must remain unassessed');
    vm.runInContext("qualityState.indicators.modules[0].sourceSHA256='different';signalSummary(document.getElementById('signal-summary'),{package:'app/service'})",ctx);
    assert.match(dom.window.document.querySelector('#signal-summary').textContent,/Not assessed.*Test Quality/s,'stale source results must not be presented as current');
    vm.runInContext("visualMetrics(document.getElementById('quality-detail'),['unknown'])",ctx);
    assert.ok(dom.window.document.querySelector('#quality-detail [data-metric=\"test-quality\"]'),'visual review must include Test Quality');
    assert.equal(dom.window.document.querySelector('#quality-execute').checked,true,'fresh execution authorization must default on');
    const start=dom.window.document.querySelector('#quality-start');
    const pending=start.onclick();
    assert.equal(start.disabled,true,'starting must immediately prevent repeated clicks');
    assert.match(dom.window.document.querySelector('#quality-notice').textContent,/starting|preparing/i,'starting must immediately acknowledge the action');
    releaseStart();delayStart=false;
    await pending;
    assert.equal(start.disabled,false,'start control should recover after the request');
    assert.equal(requests.filter(r=>r.url==='/api/test-quality/start').length,1);
    assert.equal(requests.find(r=>r.url==='/api/test-quality/start').args.allowExecution,true);
    enabled=false;
    await vm.runInContext('qualityOpen()',ctx);
    assert.equal(dom.window.document.querySelector('#quality-start').disabled,false,'AI-disabled deterministic baseline must remain available');
    dom.window.document.querySelector('#quality-execute').checked=false;
    await dom.window.document.querySelector('#quality-start').onclick();
    assert.equal(requests.filter(r=>r.url==='/api/test-quality/start').at(-1).args.allowExecution,false,'explicit opt-out was lost');
    enabled=true;
    const progress=dom.window.document.querySelector('#quality-progress-bar');
    assert.ok(progress,'running assessment has no progress indicator');
    assert.doesNotMatch(dom.window.document.querySelector('#quality-overview').textContent,/Reviewing acceptance tests/,'the overview must not duplicate agent messages shown in conversation history');
    assert.equal(progress.value,1);
    assert.equal(progress.max,3,'excluded functions must not inflate work remaining');
    assert.match(dom.window.document.querySelector('#quality-progress-label').textContent,/1 of 3 tests processed/);
    summary.counts.insufficient=1;summary.counts.unreviewed=1;
    dom.window.document.querySelector('.quality-assessment-details').open=true;
    await vm.runInContext('qualityPoll()',ctx);
    assert.equal(dom.window.document.querySelector('.quality-assessment-details').open,true,'polling must preserve expanded assessment details');
    dom.window.document.querySelector('.quality-assessment-details').open=false;
    await vm.runInContext('qualityPoll()',ctx);
    assert.equal(dom.window.document.querySelector('.quality-assessment-details').open,false,'polling must preserve collapsed assessment details');
    dom.window.document.querySelector('.quality-assessment-details').open=true;
    await vm.runInContext("qualityState.id='another-assessment';qualityOverview()",ctx);
    assert.equal(dom.window.document.querySelector('.quality-assessment-details').open,false,'another assessment must not inherit expanded details');
    vm.runInContext("qualityState.id='assessment'",ctx);
    assert.equal(progress.value,2,'insufficient results are processed work, not scored results');
    jobStatus='queued';
    await vm.runInContext('qualityPoll()',ctx);
    assert.equal(progress.hasAttribute('value'),false,'queued work must not imply active progress');
    assert.match(dom.window.document.querySelector('#quality-progress-label').textContent,/Queued/);
    jobStatus='cancelled';
    await vm.runInContext('qualityPoll()',ctx);
    assert.equal(progress.value,2,'cancellation must preserve actual progress, not show 100%');
    assert.equal(dom.window.document.querySelector('#quality-job-progress').getAttribute('aria-busy'),'false');
    jobStatus=null;
    vm.runInContext('qualityClose()',ctx);
    assert.equal(vm.runInContext('JSON.stringify({report,mode})',ctx),before,'assessment overwrote Application state');
    assert.equal(requests.some(r=>r.url==='/api/test-quality/cancel'),false,'navigation cancelled background work');
    await vm.runInContext('qualityOpen()',ctx);
    await vm.runInContext("qualityPackages()",ctx);
    const pkg=dom.window.document.querySelector('#quality-packages button');
    await pkg.onclick();
    const fn=dom.window.document.querySelector('#quality-functions button');
    await fn.onclick();
    assert.equal(vm.runInContext('chatContext().assessmentID',ctx),'assessment');
    assert.equal(vm.runInContext('chatContext().symbolID',ctx),'TestF');
    assert.equal(vm.runInContext('chatContext().operation',ctx),'test');
    assert.match(dom.window.document.querySelector('#quality-detail').textContent,/Checks outputs.*Order dependency.*Reset fixture/);
    const cite=dom.window.document.querySelector('#quality-detail .quality-citation');
    await cite.onclick();
    assert.match(dom.window.document.querySelector('#quality-source').textContent,/f_test.go.*assert.Equal/s);
    assert.equal(dom.window.document.querySelector('#quality-source img'),null,'source became executable HTML');
    narratives=false;
    await vm.runInContext(`qualityResponsibility(${JSON.stringify(identity)})`,ctx);
    assert.doesNotMatch(dom.window.document.querySelector('#quality-detail').textContent,/Consequence:|Suggested test\/improvement:|undefined/,'optional finding text became empty boilerplate');
    const production=[...dom.window.document.querySelectorAll('#quality-detail button')].find(b=>/Browse supporting production/.test(b.textContent));
    await production.onclick();
    const target=[...dom.window.document.querySelectorAll('#quality-detail button')].find(b=>b.textContent==='app/service · F');
    target.onclick();
    const coverage=[...dom.window.document.querySelectorAll('#quality-detail button')].find(b=>/Statement coverage/.test(b.textContent));
    await coverage.onclick();
    const more=[...dom.window.document.querySelectorAll('#quality-detail button')].find(b=>/More evidence/.test(b.textContent));
    assert.ok(more,'coverage pagination is inaccessible in the evidence reader');
    await more.onclick();
    assert.equal(requests.filter(r=>r.args.evidenceOperation==='coverage').at(-1).args.args.offset,1);
    running=false;
    summary.counts.scored=2;summary.counts.unreviewed=0;
    await vm.runInContext('qualityPoll()',ctx);
    assert.equal(progress.value,3);
    assert.equal(progress.max,3);
    assert.equal(dom.window.document.querySelector('#quality-job-progress').getAttribute('aria-busy'),'false');
    vm.runInContext("qualityRenderProgress('running',null)",ctx);
    assert.equal(progress.hasAttribute('value'),false,'missing totals must remain indeterminate');
    assert.match(dom.window.document.querySelector('#quality-progress-label').textContent,/total not yet available/);
    vm.runInContext("qualityRenderProgress('completed',{counts:{scored:0,insufficient:0,unreviewed:0,excluded:4}})",ctx);
    assert.equal(progress.hidden,true,'empty scope must not appear 100% assessed');
    vm.runInContext('qualityRenderProgress()',ctx);
    assert.equal(dom.window.document.querySelector('#quality-job-progress').hidden,true,'no assessment must clear stale progress');
    await vm.runInContext('qualityOverview()',ctx);
    assert.match(dom.window.document.querySelector('#quality-detail').textContent,/Shared mutable fixture/,'progress refresh discarded selected detail');
    const fingerprint=vm.runInContext('chatFingerprint()',ctx);
    vm.runInContext('qualityClose()',ctx);
    await assert.rejects(vm.runInContext(`qualityNavigate({assessmentID:'assessment',operation:'overview'},${JSON.stringify(fingerprint)})`,ctx),/changed manually/);
    assert.equal(requests.some(r=>/unit-tests|analyze|changes\/collect/.test(r.url)),false);
    external=true;
    await vm.runInContext('qualityOpen()',ctx);
    await dom.window.document.querySelector('#quality-start').onclick();
    assert.equal(requests.filter(r=>r.url==='/api/test-quality/start').length,2,'external source transfer was not gated');
    dom.window.document.querySelector('#quality-consent').checked=true;
    await dom.window.document.querySelector('#quality-start').onclick();
    assert.equal(requests.filter(r=>r.url==='/api/test-quality/start').at(-1).args.confirmExternal,true);
    vm.runInContext("qualityClose();go({kind:'application'})",ctx);
    await new Promise(resolve=>setImmediate(resolve));
    const overviewLink=dom.window.document.querySelector('.quality-overview-link');
    assert.ok(overviewLink,'Application overview does not link to saved AI test-quality assessment');
    assert.match(overviewLink.textContent,/75\.0/,'matching source score must appear on the application overview');
    vm.runInContext("report.sourceSHA256='changed';qualityApplicationScore(false)",ctx);
    assert.match(overviewLink.textContent,/Not assessed/,'a stale saved score must not be shown for the open source');
    const statusReads=requests.filter(r=>r.url==='/api/test-quality/status').length;
    vm.runInContext("qualityState.loadedReport=null;go({kind:'packages'})",ctx);
    await new Promise(resolve=>setImmediate(resolve));
    assert.ok(requests.filter(r=>r.url==='/api/test-quality/status').length>statusReads,'a new snapshot must load indicators even when browsing outside Application');
  }finally{dom.window.close();}
});

test('Test Quality pages collective and diagnostic detail without executing work',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const requests=[];
  const report={root:'/repo',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const citation={snapshotID:'pinned',fileID:'contract',startLine:1,endLine:2};
  dom.window.fetch=async(url,options)=>{
    const args=options?.body?JSON.parse(options.body):{};requests.push({url,args});
    const pages={
      expectations:[{id:'range',description:'Range contract',basis:'documented',state:'supported',conditionCount:2}],
      conditions:[{id:args.offset?'upper':'lower',description:args.offset?'Upper partition':'Lower partition',state:'gap',rationale:'Missing assertion',evidence:[citation]}],
      'suite-findings':[{title:'Suite finding',rationale:'Collective evidence',evidence:[citation]}],
      'suite-groups':[{package:'app/service',scope:[{snapshotID:'pinned',module:'app',symbolID:'TestRange'}],ratings:[{dimension:'behavior-adequacy',value:3,rationale:'Complementary checks',evidence:[citation]}],findings:[{title:'Missing failure case',rationale:'Failure is not checked',evidence:[citation]}]}],
      diagnostics:[{diagnosticIndex:0,status:'partial',sourceVerified:true,request:{reason:'Repeat a reviewed test'},runCount:1,artifactCount:1}],
      'diagnostic-runs':[{runIndex:0,status:'partial',error:'One attempt failed',attemptCount:2}],
      'diagnostic-attempts':[{test:'app::TestRange',attempt:2,outcome:'fail'}],
      'diagnostic-artifacts':[{path:'retained/events',sha256:'hash'}]
    };
    let value={};
    if(url==='/api/snapshot')value=report;
    else if(url==='/api/chat/status')value={enabled:true,external:false};
    else if(url==='/api/test-quality/status')value={assessments:[{id:'saved',status:'completed',summary:{counts:{scored:1}}}]};
    else if(args.operation==='modules')value={items:[],nextOffset:null};
    else if(args.operation==='tests')value={items:[{identity:{snapshotID:'pinned',module:'app',symbolID:'test'},name:'TestRange',package:'app',status:'scored',summary:{score:75},findingCount:0}],nextOffset:null};
    else if(args.operation==='overview')value={assessmentID:'saved',version:'test-quality/3',unit:'test',status:'completed',summary:{counts:{scored:1}},categories:{}};
    else if(args.operation==='suite')value={status:'scored'};
    else if(args.operation==='expectation')value={id:'range',description:'Range contract',basis:'documented',state:'supported',rationale:'Contract basis',scope:[{snapshotID:'pinned',module:'app',symbolID:'TestRange'}],evidence:[citation]};
    else if(pages[args.operation])value={items:pages[args.operation],nextOffset:args.operation==='conditions'&&!args.offset?1:null};
    return {ok:true,json:async()=>value};
  };
  try{
    const ctx=dom.getInternalVMContext();
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    await vm.runInContext("qualityOpen()",ctx);
    assert.match(dom.window.document.querySelector('#quality-functions').textContent,/TestRange/,'opening a saved review should show its tests without an extra selection');
    await vm.runInContext("qualityState.id='saved';qualityOverview()",ctx);
    const click=async text=>{
      const button=[...dom.window.document.querySelectorAll('#quality-overview button')].find(b=>b.textContent.includes(text));
      assert.ok(button,'missing detail control: '+text);await button.onclick();
    };
    await click('Inspect collective');
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/app\/service.*Complementary checks.*Missing failure case/s,'group judgments must remain useful before project synthesis');
    await click('Range contract');
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/Group observation.*1 selected test.*other groups may contribute/);
    await vm.runInContext('qualityPoll()',ctx);
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/Range contract/,'status refresh must not erase the evidence being read');
    assert.equal(requests.find(r=>r.args.operation==='expectation').args.expectationID,'range');
    await click('More conditions');
    assert.equal(requests.filter(r=>r.args.operation==='conditions').at(-1).args.offset,1);
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/Upper partition/);
    await click('Inspect fresh diagnostic');
    await click('Inspect diagnostic runs');
    await click('Inspect attempts');
    await click('Inspect diagnostic artifacts');
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/One attempt failed.*"outcome": "fail".*retained\/events/s);
    assert.ok(requests.some(r=>r.args.operation==='suite-findings'),'collective findings are hidden');
    assert.ok(requests.filter(r=>r.args.operation==='diagnostic-attempts').every(r=>r.args.diagnosticIndex===0&&r.args.runIndex===0));
    assert.equal(requests.some(r=>/start|unit-tests|analyze/.test(r.url)),false,'detail reads launched work');
  }finally{dom.window.close();}
});
