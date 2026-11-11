const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('activity overview groups a component responsibility without losing async work or branch evidence',()=>{
  const {endpointFlow,activityOverview}=require('./endpoint-flow-tools.js'),r=fixture();
  r.symbols.find(s=>s.id==='start').doc='Start records a job and schedules its processing.';
  const before=JSON.stringify(r),scene=endpointFlow(r,r.httpEndpoints[0]),overview=activityOverview(r,scene);
  const activity=overview.nodes.find(n=>n.symbolID==='start');
  assert.ok(activity);
  assert.match(activity.title,/records a job/i,'activity title must communicate responsibility, not just a method name');
  assert.match(activity.description,/records a job/);
  assert.ok(activity.operations.some(n=>n.symbolID==='store'),'downstream responsibility lost');
  assert.ok(!overview.nodes.some(n=>n.symbolID==='store'),'nested operation still masquerades as a peer activity');
  const work=overview.nodes.find(n=>n.phase==='async-work');
  assert.ok(work,'background work must stay visible outside its owning activity');
  assert.ok(overview.edges.some(e=>e.from===activity.id&&e.to===work.id&&e.kind==='asynchronous'));
  assert.ok(!overview.edges.some(e=>e.from===work.id),'background completion must not join the response');
  assert.deepEqual(new Set(overview.alternatives.map(a=>a.id)),new Set(['yes','no']));
  assert.equal(JSON.stringify(r),before);
  r.symbols.find(s=>s.id==='start').doc='Start records a job and schedules its processing.\n\nThe second paragraph explains why both responsibilities are necessary.';
  const documented=activityOverview(r,scene).nodes.find(n=>n.symbolID==='start');
  assert.equal(documented.description,r.symbols.find(s=>s.id==='start').doc,
    'full commentary must not be reduced to the first sentence');
});

function fixture() {
  const location=line=>({fileID:'f',line,column:1,endLine:line,endColumn:20,offset:line*20,endOffset:line*20+19});
  const symbols=['handler','helper','start','store','worker'].map((id,i)=>({
    id,name:id,kind:'function',package:'app/'+(i<2?'http':id),location:location(i+1)
  }));
  const call=(id,from,to,invocation='synchronous')=>({id,from,to,kind:'static',invocation,evaluationOrder:1,location:location(10)});
  const calls=[call('decode','handler','helper'),call('invoke','handler','start'),
    call('save','start','store'),call('launch','start','worker','goroutine')];
  const node=(id,kind,callIDs=[],line=10)=>({id,kind,callIDs,label:id,location:location(line),reachable:true,callOrderKnown:true});
  const edge=(from,to,label='',kind='sequence')=>({from,to,label,kind});
  return {root:'/repo',module:'app',sourceSHA256:'sha',packages:[],files:[{id:'f',path:'app.go',source:'package app'}],
    symbols,calls,documents:[],warnings:[],limitations:[],commands:[],httpEndpoints:[{id:'route',handlerID:'handler',pattern:'POST /jobs',patternKnown:true}],
    controlFlows:[
      {symbolID:'handler',entry:'entry',nodes:[node('entry','entry'),node('prep','statement',['decode']),node('valid','expression',[],12),
        node('start','statement',['invoke']),node('exit','exit')],edges:[edge('entry','prep'),edge('prep','valid'),
        edge('valid','start','true','branch'),edge('valid','exit','false','branch'),edge('start','exit')]},
      {symbolID:'start',entry:'entry',nodes:[node('entry','entry'),node('save','statement',['save']),node('launch','go',['launch']),node('exit','exit')],
        edges:[edge('entry','save'),edge('save','launch'),edge('launch','exit')]},
      ...['helper','store','worker'].map(symbolID=>({symbolID,entry:'entry',nodes:[node('entry','entry'),node('assignment','statement'),node('exit','exit')],
        edges:[edge('entry','assignment'),edge('assignment','exit')]}))
    ],operationSources:{callables:[],calls:[]},
    systemFlow:{sourceSHA256:'sha',components:[
      {id:'http',name:'HTTP',kind:'component',members:['handler','helper'],sources:[]},
      ...['start','store','worker'].map(id=>({id,name:id,kind:'component',members:[id],sources:[]}))
    ],operations:[],exchanges:[],alternatives:[{id:'yes',symbolID:'handler',componentID:'http',decision:'valid',label:'true',
      location:location(12),execution:'exercised',reason:'Unique-arm counter hit.',witnesses:[],requires:[]},
      {id:'no',symbolID:'handler',componentID:'http',decision:'valid',label:'false',location:location(12),
        execution:'not-observed',reason:'No hit on the exclusive arm.',witnesses:[],requires:[]}]}
  };
}

test('dependency handoffs survive dotless package paths and disclose their unreviewed implementation',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js');
  for(const origin of ['dependency',undefined]) {
    const r=fixture(),call=r.calls.find(c=>c.id==='invoke');
    delete call.to;
    call.target='func builder/handler.Handle()';
    r.operationSources.calls=[{callID:'invoke',targetName:'Handle',targetPackage:'builder/handler',
      targetStatus:'external',targetOrigin:origin,arguments:[],resultTypes:['error']}];
    const scene=endpointFlow(r,r.httpEndpoints[0]);
    const boundary=scene.nodes.find(n=>n.callID==='invoke');
    assert.ok(boundary,'dotless dependencies must not disappear, including legacy snapshots');
    assert.equal(boundary.symbolID,undefined,'never claim a local implementation was reviewed');
    assert.match(boundary.boundary,/outside.*analy|not.*analy/i);
    assert.ok(scene.limitations.some(text=>text.includes('builder/handler')));
  }
});

test('known standard-library helpers remain collapsed without hiding dependency calls',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture(),call=r.calls.find(c=>c.id==='invoke');
  delete call.to;call.target='func fmt.Sprintf()';
  r.operationSources.calls=[{callID:'invoke',targetName:'Sprintf',targetPackage:'fmt',targetStatus:'external',
    targetOrigin:'standard-library',arguments:[]}];
  assert.ok(!endpointFlow(r,r.httpEndpoints[0]).nodes.some(n=>n.callID==='invoke'));
});

test('Wasm result construction is supporting detail, not a component handoff',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js');
  for(const name of ['Ok','Err']) {
    const r=fixture(),call=r.calls.find(c=>c.id==='invoke');
    delete call.to;
    call.target='func go.bytecodealliance.org/pkg/wit/types.'+name+'()';
    r.operationSources.calls=[{callID:'invoke',targetName:name,targetPackage:'go.bytecodealliance.org/pkg/wit/types',
      targetStatus:'external',targetOrigin:'dependency',arguments:[],
      resultTypes:['go.bytecodealliance.org/pkg/wit/types.Result[go.bytecodealliance.org/pkg/wit/types.Unit, string]']}];
    const endpoint={...r.httpEndpoints[0],entryKind:'wasm'};
    const scene=endpointFlow(r,endpoint);
    assert.ok(!scene.nodes.some(n=>n.callID==='invoke'),'ABI result constructors must not dominate the overview');
    assert.deepEqual(scene.alternatives.map(a=>a.id).sort(),['no','yes']);
    assert.ok(scene.limitations.some(text=>/WIT result.*function.*source/i.test(text)));
    assert.ok(r.calls.some(c=>c.id==='invoke'),'supporting call evidence must remain retained');
    r.operationSources.calls[0].receiver={type:'go.bytecodealliance.org/pkg/wit/types.Result'};
    r.operationSources.calls[0].resultTypes=['string'];
    assert.ok(endpointFlow(r,endpoint).nodes.some(n=>n.callID==='invoke'),
      'result accessors must not be mistaken for constructors');
  }
});

test('Wasm input decoding remains visible when its record type belongs to a dependency',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  r.operationSources.callables=[{key:'helper',symbolID:'helper',kind:'function',
    results:[{value:{type:'builder/runtime.EventEnvelope'}},{value:{type:'error'}}]}];
  const scene=endpointFlow(r,{...r.httpEndpoints[0],entryKind:'wasm'});
  const preparation=scene.nodes.find(n=>n.symbolID==='helper');
  assert.ok(preparation,'the input-decoding responsibility must not disappear');
  assert.equal(preparation.kind,'Prepare information');
});

for(const kind of ['wasm','cli','library'])test(kind+' entry inventory opens the same implementation flow and retains declaration evidence',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),snapshot=fixture();
  snapshot.httpEndpoints=[];
  snapshot.supportingFiles=[{id:'binding',path:'wit_exports.go',source:'//go:wasmexport handle\nfunc wrapper() {}'}];
  snapshot.entryPoints={kind,initializers:[],limitations:[],sources:kind==='wasm'?[
    {id:'wit',path:'wit/world.wit',sha256:'witsha',text:'world app { export handle: func(); }'}]:[],
    entries:[{id:'entry',kind:kind==='cli'?'main':kind,name:kind==='cli'?'main':kind==='wasm'?'app#handle':'PublicAPI',
      symbolID:'handler',resolution:'resolved',evidence:kind==='wasm'?[{sourceID:'wit',line:1,kind:'wit-export'},
        {kind:'wrapper-call',location:{fileID:'binding',line:1,endLine:2}}]:
        [{kind:'go-declaration',location:snapshot.symbols[0].location}]}]};
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  let executions=0;
  dom.window.fetch=async(url,options)=>{
    if(options?.method==='POST')executions++;
    return {ok:true,json:async()=>url==='/api/chat/status'?{enabled:false,snapshot:{id:'snap'}}:
      url==='/api/review'?{running:false,run:null}:snapshot};
  };
  try {
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.match(doc.querySelector('#application-overview h1').textContent,
      kind==='wasm'?/Wasm component/:kind==='library'?/Public library API/:/Program entry/);
    assert.equal(doc.querySelectorAll('.endpoint-row').length,1);
    doc.querySelector('.endpoint-row').click();
    assert.ok(doc.querySelector('#activity-overview [data-symbol-id="handler"]'),'entry did not reach implementation flow');
    assert.ok(!doc.querySelector('#activity-overview').textContent.includes('HTTP handler completes'));
    const evidence=[...doc.querySelectorAll('#detail button')].find(b=>b.textContent==='Entry declaration & evidence');
    assert.ok(evidence,'entry evidence must be reachable in the visible inspector, not only the hidden explorer');
    evidence.click();
    if(kind==='wasm') {
      assert.match(doc.querySelector('#detail').textContent,/wit-export/);
      [...doc.querySelectorAll('#detail button')].find(b=>b.textContent==='Inspect binding source').click();
      assert.match(doc.querySelector('#detail pre')?.textContent||'',/go:wasmexport handle/);
      [...doc.querySelectorAll('#detail button')].find(b=>b.textContent==='Read wit/world.wit').click();
      assert.equal(doc.querySelector('.repository-document pre').textContent,snapshot.entryPoints.sources[0].text);
      await vm.runInContext('chatLoad()',dom.getInternalVMContext());
      await vm.runInContext("chatNavigate({snapshotID:'snap',view:'document',id:'wit'})",dom.getInternalVMContext());
      assert.equal(doc.querySelector('.repository-document pre').textContent,snapshot.entryPoints.sources[0].text);
    }
    assert.equal(executions,0);
  }finally{dom.window.close();}
});

test('entry overview follows component handoffs and control alternatives without statement cards',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js');
  const r=fixture(),before=JSON.stringify(r),scene=endpointFlow(r,r.httpEndpoints[0]);
  assert.ok(scene.nodes.some(n=>n.symbolID==='start'),'business operation missing');
  assert.ok(scene.nodes.some(n=>n.symbolID==='store'),'downstream handoff must be visible without expanding functions');
  assert.ok(scene.nodes.some(n=>n.symbolID==='worker'),'asynchronous work missing');
  assert.ok(!scene.nodes.some(n=>n.label==='assignment'||n.symbolID==='helper'),'supporting code must stay collapsed');
  assert.ok(scene.edges.some(e=>e.alternatives?.some(a=>a.id==='yes')));
  assert.ok(scene.edges.some(e=>e.alternatives?.some(a=>a.id==='no')));
  const launch=scene.nodes.find(n=>n.callID==='launch');
  assert.ok(scene.edges.some(e=>e.from===launch.id&&e.kind==='asynchronous'));
  assert.ok(!scene.edges.some(e=>e.kind==='return-to-caller'&&e.from.includes('launch')),'async work must not join synchronous continuation');
  assert.equal(JSON.stringify(r),before);
});

test('overview preserves repeated occurrences, recursion boundaries and missing-source diagnostics',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js');
  const r=fixture(),f=r.controlFlows[0];
  r.calls.push({...r.calls.find(c=>c.id==='invoke'),id:'again'});
  f.nodes.splice(-1,0,{...f.nodes.find(n=>n.id==='start'),id:'again',callIDs:['again']});
  f.edges=f.edges.filter(e=>e.from!=='start');
  f.edges.push({from:'start',to:'again'},{from:'again',to:'exit'});
  let scene=endpointFlow(r,r.httpEndpoints[0]);
  assert.equal(scene.nodes.filter(n=>n.symbolID==='start'&&n.phase==='operation').length,2);
  r.calls.find(c=>c.id==='save').to='start';
  scene=endpointFlow(r,r.httpEndpoints[0]);
  assert.ok(scene.limitations.some(s=>/recurs/i.test(s)));
  assert.ok(endpointFlow({...r,systemFlow:null},r.httpEndpoints[0]).error);
  assert.ok(endpointFlow({...r,systemFlow:{...r.systemFlow,sourceSHA256:'old'}},r.httpEndpoints[0]).error);
});

test('transport helpers stay processing stages and their implementation branches do not overwhelm the entry flow',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js');
  const r=fixture();
  r.operationSources.callables=[{key:'helper',symbolID:'helper',kind:'function',parameters:[{value:{type:'net/http.ResponseWriter'}}]}];
  r.systemFlow.alternatives.push({...r.systemFlow.alternatives[0],id:'helper-branch',symbolID:'helper'});
  const before=JSON.stringify(r),scene=endpointFlow(r,r.httpEndpoints[0]);
  const helper=scene.nodes.find(n=>n.symbolID==='helper');
  assert.ok(helper,'request/response work should remain one processing stage');
  assert.equal(helper.kind,'Request / response');
  assert.equal(JSON.stringify(r),before,'projection must never mutate retained source facts');
});

test('contracts use concrete call-site result types rather than generic type parameters',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  r.operationSources.callables=[{key:'helper',symbolID:'helper',kind:'function',
    parameters:[{value:{type:'*net/http.Request'}}],results:[{position:0,value:{type:'T'}}]}];
  r.operationSources.calls=[{callID:'decode',targetKey:'helper',resultTypes:['app/http.Request']}];
  const stage=endpointFlow(r,r.httpEndpoints[0]).nodes.find(n=>n.symbolID==='helper');
  assert.ok(stage.information.some(i=>i.direction==='output'&&i.type==='app/http.Request'));
  r.symbols.find(s=>s.id==='helper').doc='helper decodes a T from the request.';
  const activity=require('./endpoint-flow-tools.js').activityOverview(r,endpointFlow(r,r.httpEndpoints[0]))
    .nodes.find(n=>n.symbolID==='helper');
  assert.match(activity.description,/app\/http.Request/,'generic documentation must use the instantiated contract');
});

test('deferred component operations remain visible as scheduling rather than execution',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  r.systemFlow.components.find(c=>c.id==='start').members.push('store');
  r.systemFlow.components=r.systemFlow.components.filter(c=>c.id!=='store');
  r.calls.find(c=>c.id==='save').invocation='deferred';
  const stage=endpointFlow(r,r.httpEndpoints[0]).nodes.find(n=>n.callID==='save');
  assert.equal(stage?.kind,'Schedule deferred work');
  assert.equal(stage.invocation,'deferred');
});

test('callback arguments expose the actual background operation without inventing a response join',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  const owner=r.symbols.find(s=>s.id==='start');
  r.symbols.push({...owner,id:'launcher',name:'launch',kind:'method'},
    {...owner,id:'wrapper',name:'func literal',kind:'closure'},
    {...owner,id:'callback',name:'callback',kind:'closure',location:{fileID:'f',offset:700,endOffset:800}});
  r.systemFlow.components.find(c=>c.id==='start').members.push('launcher','wrapper','callback');
  const original=r.calls.find(c=>c.id==='launch');original.to='launcher';original.invocation='synchronous';
  r.calls.push({id:'go',from:'launcher',to:'wrapper',kind:'static',invocation:'goroutine'},
    {id:'work',from:'wrapper',kind:'indirect',invocation:'synchronous'},
    {id:'forward',from:'callback',to:'worker',kind:'static',invocation:'synchronous'});
  const mini=(symbolID,id)=>({symbolID,entry:'e',nodes:[
    {id:'e',kind:'entry',callIDs:[]},{id:'call',kind:'statement',callIDs:[id],callOrderKnown:true},{id:'x',kind:'exit',callIDs:[]}],
    edges:[{from:'e',to:'call'},{from:'call',to:'x'}]});
  r.controlFlows.push(mini('launcher','go'),mini('wrapper','work'),mini('callback','forward'));
  r.operationSources.callables=[{key:'launcher',symbolID:'launcher',parameters:[
    {position:0,value:{objectKey:'callback-slot',type:'func()'}}]}];
  r.operationSources.calls=[{callID:'launch',targetKey:'launcher',arguments:[{
    parameterPosition:0,value:{type:'func()',anchor:{location:{fileID:'f',offset:700,endOffset:800}}}}]},
    {callID:'work',targetKey:'callback-slot',targetStatus:'function-value'}];
  const scene=endpointFlow(r,r.httpEndpoints[0]);
  const background=scene.nodes.find(n=>n.phase==='async-work');
  assert.equal(background.symbolID,'worker');
  assert.equal(background.resolution,'inferred');
  assert.equal(scene.edges.filter(e=>e.from===background.id).length,0);
  r.symbols.push({...owner,id:'otherworker',name:'other worker'},
    {...owner,id:'othercallback',kind:'closure',location:{fileID:'f',offset:900,endOffset:1000}});
  r.calls.push({...original,id:'other-launch',evaluationOrder:2},
    {id:'other-forward',from:'othercallback',to:'otherworker',kind:'static',invocation:'synchronous'});
  r.controlFlows.find(f=>f.symbolID==='start').nodes.find(n=>n.id==='launch').callIDs.push('other-launch');
  r.controlFlows.push(mini('othercallback','other-forward'));
  r.operationSources.calls.push({...r.operationSources.calls[0],callID:'other-launch',arguments:[{
    parameterPosition:0,value:{type:'func()',anchor:{location:{fileID:'f',offset:900,endOffset:1000}}}}]});
  const isolated=endpointFlow(r,r.httpEndpoints[0]);
  assert.deepEqual(isolated.nodes.filter(n=>n.phase==='async-work').map(n=>n.symbolID),['worker','otherworker'],
    'callback bindings must not leak between calls to the same launcher');
});

test('unknown expression order stays an explicit boundary with no invented call sequence',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  const node=r.controlFlows[0].nodes.find(n=>n.id==='start');
  node.callIDs=['invoke','decode'];node.callOrderKnown=false;
  const scene=endpointFlow(r,r.httpEndpoints[0]),boundary=scene.nodes.find(n=>n.phase==='unordered');
  assert.ok(boundary);
  assert.deepEqual(boundary.callIDs,['invoke','decode']);
  assert.ok(scene.limitations.some(s=>s.includes('Expression call order')));
  assert.ok(!scene.nodes.some(n=>n.symbolID==='start'));
});

test('unresolved callbacks and missing component control flow stay explicit boundaries',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture();
  const invoke=r.calls.find(c=>c.id==='invoke');
  delete invoke.to;invoke.kind='indirect';invoke.target='work';
  let scene=endpointFlow(r,r.httpEndpoints[0]);
  assert.ok(scene.nodes.some(n=>n.resolution==='unresolved'&&n.callID==='invoke'));
  invoke.to='start';invoke.kind='static';
  r.controlFlows=r.controlFlows.filter(f=>f.symbolID!=='start');
  scene=endpointFlow(r,r.httpEndpoints[0]);
  assert.ok(scene.nodes.find(n=>n.symbolID==='start').boundary);
});

test('contracting hidden loops terminates without discarding exits or branch evidence',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture(),cfg=r.controlFlows[0];
  cfg.edges.find(e=>e.from==='start').to='valid';
  const scene=endpointFlow(r,r.httpEndpoints[0]);
  const completion=scene.nodes.find(n=>n.phase==='completion');
  assert.ok(completion);
  assert.ok(scene.edges.some(e=>e.to===completion.id&&e.alternatives.some(a=>a.id==='no')));
  const business=scene.nodes.find(n=>n.symbolID==='start');
  assert.ok(scene.edges.some(e=>e.to===business.id&&e.alternatives.some(a=>a.id==='yes')));
  assert.ok(scene.nodes.length<20);
});

test('converging source alternatives are coalesced without enumerating path combinations',()=>{
  const {endpointFlow}=require('./endpoint-flow-tools.js'),r=fixture(),cfg=r.controlFlows[0];
  const entry=cfg.nodes[0],operation=cfg.nodes.find(n=>n.id==='start'),exit=cfg.nodes.at(-1);
  cfg.nodes=[entry,operation,exit];cfg.edges=[{from:'entry',to:'g0'},{from:'start',to:'exit'}];
  for(let i=0;i<18;i++) {
    const next=i===17?'repeat':'g'+(i+1);
    for(const id of ['g'+i,'t'+i,'f'+i])
      cfg.nodes.push({...entry,id,kind:'expression',label:id});
    cfg.edges.push({from:'g'+i,to:'t'+i,label:'true'},{from:'g'+i,to:'f'+i,label:'false'},
      {from:'t'+i,to:next},{from:'f'+i,to:next});
  }
  cfg.nodes.push({...entry,id:'repeat',kind:'expression',label:'repeat'});
  cfg.edges.push({from:'repeat',to:'g0',label:'true'},{from:'repeat',to:'start',label:'false'});
  const start=performance.now(),scene=endpointFlow(r,r.httpEndpoints[0]),elapsed=performance.now()-start;
  assert.ok(elapsed<2000,'source contraction took '+Math.round(elapsed)+'ms for 18 joined diamonds');
  const edge=scene.edges.find(e=>e.from===scene.entry&&scene.nodes.find(n=>n.id===e.to)?.symbolID==='start');
  assert.equal(edge.guards.length,38,'both alternatives at 18 decisions and the loop decision must survive');
});

test('endpoint selection opens overall flow, stage evidence and optional function detail with Back',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),snapshot=fixture();
  snapshot.symbols.find(s=>s.id==='start').doc='Start records a job and schedules its processing.\n\nEvery request retains its original identity.';
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  let executions=0;
  dom.window.fetch=async(url,options)=>{
    if(options?.method==='POST')executions++;
    return {ok:true,json:async()=>url==='/api/chat/status'?{enabled:false,snapshot:{id:'snap'}}:
      url==='/api/review'?{running:false,run:null}:snapshot};
  };
  try {
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    assert.ok(dom.window.document.querySelector('.entry-group [data-metric="coverage"]'),
      'entry inventory needs the same comparable metrics as the flow');
    assert.ok(dom.window.document.querySelector('#application-overview #endpoint-filter'),
      'filter must remain reachable without a hidden explorer');
    const tools=dom.window.document.querySelector('#workspace-tools');tools.click();
    assert.equal(dom.window.document.querySelector('#advanced-tools').open,true);
    tools.click();
    dom.window.document.querySelector('.endpoint-row').click();
    const doc=dom.window.document;
    assert.match(doc.querySelector('#breadcrumb').textContent,/overall flow/i);
    assert.ok(doc.querySelector('#activity-overview:not([hidden])'),'overall flow must stay primary');
    assert.ok(doc.querySelector('#activity-overview .review-edges path[data-from]'),'relationships must be visible, not To-N buttons');
    assert.ok(doc.querySelector('#activity-overview [data-metric="coverage"]'),'scope coverage must be simultaneously visible');
    assert.ok(doc.querySelector('#activity-overview [data-metric="complexity"]'));
    assert.ok(doc.querySelector('#activity-overview [data-metric="architecture"]'));
    assert.ok(doc.body.classList.contains('activity-review'),'review must allocate the viewport to the activity workspace');
    const setup=doc.querySelector('#workspace-setup');
    assert.ok(setup);setup.click();
    assert.ok(doc.body.classList.contains('review-setup-open'),'review controls must remain reachable');
    setup.click();
    const activity=doc.querySelector('#activity-overview [data-symbol-id="start"] button');
    assert.ok(activity,'business activity inaccessible');activity.click();
    assert.match(doc.querySelector('#detail').textContent,/store/,'downstream operation evidence inaccessible');
    assert.ok(doc.querySelector('#detail').textContent.includes(snapshot.symbols.find(s=>s.id==='start').doc),
      'complete multi-paragraph commentary must survive projection and selection');
    const compound=doc.querySelector('#activity-overview [data-symbol-id="start"]');
    const expand=compound.querySelector('.review-expand');
    assert.ok(expand,'expansion must be available on the diagram');
    expand.click();
    assert.equal(doc.querySelector('#activity-overview [data-symbol-id="start"] .review-expand').getAttribute('aria-expanded'),'true');
    assert.ok(doc.querySelector('#activity-overview [data-symbol-id="start"] .review-children [data-symbol-id="store"]'),
      'details must be revealed inside their parent context');
    assert.ok(doc.querySelector('#activity-overview [data-symbol-id="worker"]'),'background context lost on expansion');
    const focus=doc.querySelector('#activity-overview [data-symbol-id="start"] .review-node-actions button:last-child');
    focus.click();
    assert.ok(doc.querySelector('.review-parent-flow .review-parent-edge'),'focused detail needs a connected parent overview');
    assert.match(doc.querySelector('.review-parent-flow').textContent,/POST \/jobs/);
    const parentSpine=[...doc.querySelectorAll('.review-parent-main')].map(n=>n.getAttribute('transform'));
    assert.ok(parentSpine.length>1,'parent map must retain the straight main continuation');
    assert.equal(new Set(parentSpine.map(t=>t.match(/translate\(([^,]+)/)[1])).size,1,
      'parent-map spine must stay in one column');
    doc.querySelector('#back').click();
    assert.equal(doc.querySelector('#activity-overview [data-symbol-id="start"] .review-expand').getAttribute('aria-expanded'),'true');
    await vm.runInContext('chatLoad()',dom.getInternalVMContext());
    await vm.runInContext("chatNavigate({snapshotID:'snap',view:'flow',entryID:'route',focusSymbolID:'start'})",dom.getInternalVMContext());
    assert.ok(doc.querySelector('.review-parent-flow'),'chat focus must use the existing deeper flow view');
    doc.querySelector('#back').click();
    assert.equal(doc.querySelector('#activity-overview [data-symbol-id="start"] .review-expand').getAttribute('aria-expanded'),'true',
      'Back after one chat action must restore the prior expanded diagram, not an intermediate reconstruction');
    const click=name=>{
      const b=[...doc.querySelectorAll('button')].find(b=>b.textContent===name);
      assert.ok(b,'missing '+name);b.click();
    };
    assert.match(doc.querySelector('#activity-overview').textContent,/exercised|not-observed/);
    click('Inspect function steps');
    assert.match(doc.querySelector('#legend').textContent,/Possible execution flow/);
    doc.querySelector('#back').click();
    assert.match(doc.querySelector('#breadcrumb').textContent,/overall flow/i);
    assert.equal(doc.querySelector('#activity-overview [data-symbol-id="start"] .review-expand').getAttribute('aria-expanded'),'true',
      'Back must restore expansion, not just endpoint identity');
    doc.querySelector('#function-flow').click();
    assert.equal(vm.runInContext('mode.id',dom.getInternalVMContext()),'start',
      'global function navigation must use the selected activity rather than the entry handler');
    doc.querySelector('#back').click();
    doc.querySelector('#application').click();
    vm.runInContext("report.symbols[0].name='init';",dom.getInternalVMContext());
    click('Startup & wiring');
    const startup=doc.querySelector('#application-overview .startup-entry');
    assert.ok(startup,'startup entry missing');startup.click();
    assert.match(doc.querySelector('#breadcrumb').textContent,/Overall flow/);
    assert.ok(doc.querySelector('#activity-overview').textContent.includes('Startup operation completes'));
    vm.runInContext('report={...report,systemFlow:null};render();',dom.getInternalVMContext());
    assert.equal(doc.querySelectorAll('#activity-overview .review-node').length,0,
      'unavailable source facts must not leave the previous activity flow visible');
    assert.match(doc.querySelector('#detail').textContent,/Overall flow unavailable/);
    assert.equal(executions,0);
  }finally{dom.window.close();}
});
