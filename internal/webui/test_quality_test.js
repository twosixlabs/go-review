const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('Test Quality launches explicitly, retains background context and drills into actual cited source',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const requests=[];
  const report={root:'/repo',module:'app',sourceSHA256:'ordinary',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const identity={snapshotID:'pinned',module:'app',symbolID:'TestF'};
  const summary={score:75,counts:{scored:1,unreviewed:2,insufficient:0,excluded:4},partial:true};
  let running=false,exists=false,external=false,jobStatus=null,enabled=true,narratives=true;
  dom.window.fetch=async(url,options)=>{
    const args=options?.body?JSON.parse(options.body):{};
    requests.push({url,args});
    let value;
    if(url==='/api/snapshot')value=report;
    else if(url==='/api/review')value={running:false,run:null};
    else if(url==='/api/chat/status')value={enabled,external,baseURL:'http://localhost:9000',model:'test',snapshot:{id:'ordinary'}};
    else if(url==='/api/test-quality/start'){exists=true;running=true;value={id:'assessment',status:'running'};}
    else if(url==='/api/test-quality/status')value={assessments:exists?[{id:'assessment',status:jobStatus||(running?'running':'completed'),summary}]:[]};
    else if(args.operation==='overview')value={assessmentID:'assessment',status:jobStatus||(running?'running':'completed'),version:'test-quality/3',model:'test',summary,categories:{'assertion-strength':75},limitations:['Statement coverage is not assertion quality.'],moduleCount:1};
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
    assert.equal(dom.window.document.querySelector('#quality-execute').checked,true,'fresh execution authorization must default on');
    await dom.window.document.querySelector('#quality-start').onclick();
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
    assert.equal(progress.value,1);
    assert.equal(progress.max,3,'excluded functions must not inflate work remaining');
    assert.match(dom.window.document.querySelector('#quality-progress-label').textContent,/1 of 3 tests processed/);
    summary.counts.insufficient=1;summary.counts.unreviewed=1;
    await vm.runInContext('qualityPoll()',ctx);
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
    assert.match(overviewLink.textContent,/AI.*75\.0/);
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
      diagnostics:[{diagnosticIndex:0,status:'partial',sourceVerified:true,request:{reason:'Repeat a reviewed test'},runCount:1,artifactCount:1}],
      'diagnostic-runs':[{runIndex:0,status:'partial',error:'One attempt failed',attemptCount:2}],
      'diagnostic-attempts':[{test:'app::TestRange',attempt:2,outcome:'fail'}],
      'diagnostic-artifacts':[{path:'retained/events',sha256:'hash'}]
    };
    let value={};
    if(url==='/api/snapshot')value=report;
    else if(url==='/api/chat/status')value={enabled:true,external:false};
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
    await vm.runInContext("qualityState.id='saved';qualityOverview()",ctx);
    const click=async text=>{
      const button=[...dom.window.document.querySelectorAll('#quality-overview button')].find(b=>b.textContent.includes(text));
      assert.ok(button,'missing detail control: '+text);await button.onclick();
    };
    await click('Inspect collective');
    await click('Range contract');
    assert.match(dom.window.document.querySelector('#quality-overview').textContent,/Group observation.*1 selected test.*other groups may contribute/);
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
