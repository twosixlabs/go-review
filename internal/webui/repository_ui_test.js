const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('initial snapshot and review-status responses keep the module picker synchronized in either order',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  for(const snapshotFirst of [false,true]) {
    const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
    const snapshot={root:'/repo/server',module:'app',review:{status:'attention'},packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
    const run={root:'/repo',directory:'/retained',status:'attention',modules:[
      {directory:'/repo/wasm',module:'wasm'},{directory:'/repo/server',module:'app'}],steps:[]};
    let release;
    const delayed=new Promise(resolve=>{release=resolve;});
    dom.window.fetch=async url=>{
      if((url==='/api/snapshot')!==snapshotFirst)await delayed;
      return {ok:true,json:async()=>url==='/api/review'?{running:false,run}:snapshot};
    };
    try {
      for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
        vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
      await new Promise(resolve=>setImmediate(resolve));
      release();
      await new Promise(resolve=>setImmediate(resolve));
      assert.equal(dom.window.document.querySelector('#review-modules').value,snapshot.root,
        'module picker disagrees with the loaded snapshot after reload');
      assert.doesNotMatch(dom.window.document.querySelector('#status').textContent,/attention|retained/,
        'late snapshot response must not restore the obsolete banner');
    }finally{dom.window.close();}
  }
});

test('scope editor saves explicit exclusions without running or refiltering a retained review',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const styles=dom.window.document.createElement('style');
  styles.textContent=fs.readFileSync(path.join(__dirname,'style.css'),'utf8');
  dom.window.document.head.append(styles);
  const snapshot={root:'/repo',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const requests=[];
  let saved={root:'/repo',excludePaths:['cmd/bench-*'],excludeTests:['^TestSmoke']};
  dom.window.fetch=async(url,options)=>{
    requests.push({url,options});
    if(url.startsWith('/api/review/scope')){
      if(options?.method==='POST')saved=JSON.parse(options.body).scope;
      return {ok:true,json:async()=>saved};
    }
    return {ok:true,json:async()=>url==='/api/review'?{running:false,run:null}:snapshot};
  };
  try{
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    doc.querySelector('#directory').value='/repo';
    assert.equal(doc.querySelector('#scope-verification').checked,true,'new scopes include repository verification');
    assert.ok(doc.querySelector('#review-scope'),'review needs a discoverable scope editor');
    assert.equal(dom.window.getComputedStyle(doc.querySelector('.review-run')).display,'flex','applied scope must remain visible for standalone analysis results');
    doc.querySelector('#scope-load').click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(doc.querySelector('#scope-paths').value,'cmd/bench-*');
    assert.equal(doc.querySelector('#scope-verification').checked,false);
    doc.querySelector('#scope-paths').value='cmd/bench-*\n**/smoke_test.go\n';
    doc.querySelector('#scope-unit-tests').value='app::TestGood,TestReject';
    doc.querySelector('#scope-save').click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.deepEqual(saved.excludePaths,['cmd/bench-*','**/smoke_test.go']);
    assert.deepEqual(saved.unitSelections,[{package:'app',tests:['TestGood','TestReject']}]);
    assert.equal(saved.verifyRepository,false);
    assert.match(doc.querySelector('#scope-status').textContent,/next review/i);
    assert.equal(requests.filter(r=>r.options?.method==='POST'&&r.url!=='/api/review/scope').length,0);
    assert.match(doc.querySelector('#review-scope').textContent,/enabled by default in new scopes/i);
    dom.window.fetch=async()=>({ok:false,json:async()=>({error:'invalid test exclusion'})});
    doc.querySelector('#scope-save').click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.match(doc.querySelector('#scope-status').textContent,/invalid test exclusion/);
  }finally{dom.window.close();}
});

test('idle page detects a new review, survives status errors, and loads completion without executing again',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const snapshot={root:'/repo/server',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const run={root:'/repo',directory:'/retained/new',status:'attention',modules:[{directory:'/repo/server',module:'app'}],steps:[]};
  let state={running:false,run:null};
  const timers=new Map();
  let nextTimer=0,failed=false,missingSnapshot=false,posts=0,snapshotReads=0;
  dom.window.setInterval=fn=>{timers.set(++nextTimer,fn);return nextTimer;};
  dom.window.clearInterval=id=>timers.delete(id);
  dom.window.fetch=async(url,options)=>{
    if(options?.method==='POST')posts++;
    if(url==='/api/review') {
      if(failed)throw Error('connection lost');
      return {ok:true,json:async()=>state};
    }
    snapshotReads++;
    if(missingSnapshot)return {ok:false,status:404};
    return {ok:true,json:async()=>snapshot};
  };
  const tick=async()=>{
    for(const fn of [...timers.values()])await fn();
    await new Promise(resolve=>setImmediate(resolve));
  };
  try {
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.equal(doc.querySelector('#review-progress').hidden,true);
    state={running:true,progress:{phase:'Repository verification',module:'/repo',directory:'/retained/new',completedStages:2,totalStages:13,startedAt:new Date(Date.now()-65000).toISOString(),
      command:{executable:'bash',args:['/repo/scripts/verify-repository.sh'],directory:'/repo',startedAt:new Date().toISOString()}},run:null};
    await tick();
    assert.equal(doc.querySelector('#review-progress')?.hidden,false,'active review needs a persistent visible panel after reopening');
    assert.match(doc.querySelector('#review-progress').textContent,/Repository verification/);
    const bar=doc.querySelector('#review-progress progress');
    assert.equal(bar.value,2);
    assert.equal(bar.max,13);
    assert.match(doc.querySelector('#review-progress').textContent,/2 of 13 stages complete.*15%/);
    assert.match(doc.querySelector('#review-progress').textContent,/bash.*verify-repository\.sh/,'the command must be visible, not just a generic stage');
    assert.match(doc.querySelector('#review-progress').textContent,/1m 0?5s/,'elapsed time must survive reconnection');
    assert.ok(doc.querySelector('#review-repository').disabled,'reopening must not allow duplicate execution');
    assert.match(doc.querySelector('#review-repository').textContent,/running/i);
    assert.ok(timers.size,'reopened page must continue monitoring, not just fetch history once');
    failed=true;
    await tick();
    assert.match(doc.querySelector('#review-progress').textContent,/connection lost/);
    assert.ok(doc.querySelector('#review-repository').disabled,'lost status connection does not establish completion');
    failed=false;
    state={...state,progress:{...state.progress,phase:'Go tests and statement coverage',module:'/repo/server',completedStages:7,command:null}};
    await tick();
    assert.match(doc.querySelector('#review-progress').textContent,/Go tests and statement coverage/);
    assert.equal(bar.value,7);
    assert.match(doc.querySelector('#review-progress').textContent,/7 of 13 stages complete.*54%/);
    assert.ok(doc.querySelector('#review-command').hidden,'completed commands must not remain labeled as running');
    assert.doesNotMatch(doc.querySelector('#review-progress').textContent,/connection lost/);
    state={running:false,progress:state.progress,run};
    await tick();
    assert.equal(doc.querySelector('#review-progress').hidden,true);
    assert.equal(doc.querySelector('#review-repository').disabled,false);
    assert.match(doc.querySelector('#status').textContent,/Review complete/);
    assert.ok(snapshotReads>=2,'completion from another tab must load the new snapshot');
    assert.equal(posts,0,'monitoring must never execute repository code');
    assert.equal(timers.size,1,'idle monitoring must detect subsequent reviews without accumulating timers');
    const finishedStatus=doc.querySelector('#status').textContent;
    doc.querySelector('#status').textContent='a later action failed';
    await tick();
    assert.equal(doc.querySelector('#status').textContent,'a later action failed','unchanged history must not overwrite more recent feedback');
    assert.match(finishedStatus,/Review complete/);
    state={running:true,progress:{phase:'Source analysis'}};
    await dom.window.eval('pollReview()');
    assert.equal(bar.hasAttribute('value'),false,'unknown discovery totals must not imply measured progress');
    doc.querySelector('#graph').append(doc.createTextNode('previous snapshot'));
    missingSnapshot=true;
    state={running:false,run:{...run,modules:[{directory:'/repo/server',error:'analysis failed'}]}};
    await tick();
    assert.equal(doc.querySelector('#graph').textContent,'','an unavailable completed snapshot must not leave the previous graph visible');
    assert.match(doc.querySelector('#detail').textContent,/analysis failed/);
  } finally {dom.window.close();}
});

test('Review repository is one explicit action with retained results and no automatic execution on browse',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const report={root:'/repo/server',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const run={root:'/repo',directory:'/retained/run',status:'attention',modules:[{directory:'/repo/server',module:'app'},{directory:'/repo/broken',error:'invalid go.mod'}],steps:[{id:'tests',title:'Go tests',status:'fail',detail:'assertion failed',module:'/repo/server'}]};
  let release;
  const requested=[];
  dom.window.fetch=async(url,options)=>{
    requested.push({url,options});
    if(url==='/api/review'&&options?.method==='POST')return new Promise(resolve=>{release=()=>resolve({ok:true,json:async()=>({run,report})});});
    if(url==='/api/review')return {ok:true,json:async()=>({running:false,run:null})};
    return {ok:true,json:async()=>report};
  };
  try {
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.equal(requested.filter(r=>r.options?.method==='POST').length,0,'opening results must not execute a review');
    assert.equal(doc.querySelector('#review-repository')?.textContent,'Review repository');
    doc.querySelector('#directory').value='/repo';
    doc.querySelector('#open').dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
    assert.ok(doc.querySelector('#review-repository').disabled,'duplicate reviews must be disabled while collecting');
    const request=requested.find(r=>r.options?.method==='POST');
    assert.equal(request.url,'/api/review');
    assert.deepEqual(JSON.parse(request.options.body),{directory:'/repo',execute:true});
    release();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(doc.querySelector('#directory').value,'/repo','review must retain repository scope rather than silently switch to its first module');
    assert.match(doc.querySelector('#status').textContent,/1 failed check.*1 module unavailable/i);
    assert.equal(doc.querySelector('#review-modules').options.length,2);
    assert.ok(doc.querySelector('#review-modules').options[1].disabled,'failed modules must remain visible');
    doc.querySelector('#review-results').click();
    assert.match(doc.querySelector('#detail').textContent,/assertion failed/);
    assert.match(doc.querySelector('#detail').textContent,/invalid go.mod/);
    assert.doesNotMatch(doc.querySelector('#detail').textContent,/\/retained\/run/);
    assert.equal(requested.filter(r=>r.options?.method==='POST').length,1,'browsing diagnostics reran repository code');
    dom.window.fetch=async()=>({ok:false,json:async()=>({error:'retained module unavailable'})});
    doc.querySelector('#review-modules').dispatchEvent(new dom.window.Event('change'));
    await new Promise(resolve=>setImmediate(resolve));
    assert.match(doc.querySelector('#status').textContent,/retained module unavailable/,'module-selection errors must not be overwritten with a successful run summary');
    assert.equal(doc.querySelector('#review-repository').disabled,false);
    doc.querySelector('#open').dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
    await new Promise(resolve=>setImmediate(resolve));
    assert.match(doc.querySelector('#status').textContent,/retained module unavailable/,'explicit review rejection must retain its actual error');
    assert.equal(doc.querySelector('#review-progress').hidden,true,'explicit rejection must not leave the UI stuck running');
  } finally {dom.window.close();}
});

test('skipped checks are explained without an error banner or cache paths',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const snapshot={root:'/repo',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const run={root:'/repo',directory:'/old/cache/path',status:'attention',modules:[{directory:'/repo',module:'app'}],
    steps:[{id:'tests',title:'Go tests',status:'not-run',detail:'A component-aware harness is not configured.'}]};
  dom.window.fetch=async url=>({ok:true,json:async()=>url==='/api/review'?{running:false,run}:snapshot});
  try {
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    const status=doc.querySelector('#status');
    assert.doesNotMatch(status.textContent,/attention|retained|\/old\/cache/);
    assert.match(status.textContent,/1 check not run/);
    assert.equal(status.classList.contains('error'),false,'skipping unsupported checks is not a failed review');
    doc.querySelector('#review-results').click();
    assert.match(doc.querySelector('#detail').textContent,/component-aware harness is not configured/);
    assert.doesNotMatch(doc.querySelector('#detail').textContent,/\/old\/cache/);
  }finally{dom.window.close();}
});
