const test=require('node:test');
const assert=require('node:assert/strict');
const query=require('./review-query-tools.js');
const system=require('./system-flow-tools.js');

function fixture() {
  const location={fileID:'f',line:2,column:1,endLine:4,endColumn:2,offset:12,endOffset:42};
  return {module:'sample',root:'/not-readable',sourceSHA256:'sha',generatedAt:'2026-01-01',
    packages:[{path:'sample',name:'main',doc:'Runs a sample service.'}],
    files:[{id:'f',package:'sample',path:'main.go',source:'package main\nfunc main() {\n  println("λ")\n}\n'}],
    symbols:[{id:'main',name:'main',kind:'function',package:'sample',doc:'Serve requests.\n\nComplete rationale.',
      comments:[{text:'Preserve this explanation.',location}],location,cyclomatic:2}],
    documents:[{id:'readme',path:'README.md',sha256:'docsha',text:'# Purpose\n\nComplete documentation.'}],
    httpEndpoints:[],calls:[],controlFlows:[{symbolID:'main',entry:'entry',
      nodes:[{id:'entry',kind:'entry',callIDs:[],label:'entry',location},
        {id:'exit',kind:'exit',callIDs:[],label:'exit',location}],edges:[{from:'entry',to:'exit'}]}],
    systemFlow:{sourceSHA256:'sha',components:[],exchanges:[],alternatives:[]},
    testSources:[],warnings:[],limitations:[],commands:[]};
}

test('overview exposes navigable entries and complete documents without source dumps',()=>{
  const r=fixture(),before=JSON.stringify(r),v=query.query(r,'overview',{});
  assert.equal(v.module,'sample');
  assert.equal(v.startup[0].id,'startup:main');
  assert.equal(v.documents.items[0].id,'readme');
  assert.equal(v.purposeDocument.id,'readme');
  assert.equal(v.documents.items[0].text,undefined);
  assert.equal(query.query(r,'document',{id:'readme'}).text,r.documents[0].text);
  assert.equal(JSON.stringify(r),before);
});

test('flow uses UI projection and contextual inspection preserves comments and scopes',()=>{
  const r=fixture(),context={entryID:'startup:main'};
  const flow=query.query(r,'flow',context);
  assert.ok(flow.nodes.length);
  assert.equal(flow.nodes[0].descriptionLength,r.symbols[0].doc.length);
  const node=query.query(r,'inspect',{...context,nodeID:flow.nodes[0].id});
  assert.equal(query.query(r,'inspect',{...context,nodeID:flow.nodes[0].id,section:'documentation'}).text,r.symbols[0].doc);
  const commentPage=query.query(r,'inspect',{...context,nodeID:flow.nodes[0].id,section:'comments'});
  assert.equal(commentPage.total,r.symbols[0].comments.length);
  assert.deepEqual(commentPage.items.map(c=>query.query(r,'inspect',{...context,nodeID:flow.nodes[0].id,
    section:'comments',commentIndex:c.index})),r.symbols[0].comments);
  assert.deepEqual(query.query(r,'inspect',{...context,nodeID:flow.nodes[0].id,section:'members'}).items,['main']);
  assert.equal(node.metrics.complexity.value,2);
  assert.equal(node.metrics.coverage.value,null);
  assert.throws(()=>query.query(r,'flow',{entryID:'missing'}),/entry/i);
  assert.throws(()=>query.query(r,'flow',{...context,expandedIDs:['missing']}),/expanded/i);
  assert.throws(()=>query.query(r,'flow',{...context,focusSymbolID:'missing'}),/focus/i);
  assert.throws(()=>query.query({...r,systemFlow:{sourceSHA256:'old'}},'flow',context),/snapshot/i);
});

test('source reads retained Unicode lines only and rejects arbitrary paths and ranges',()=>{
  const r=fixture();
  assert.equal(query.query(r,'source',{fileID:'f',startLine:2,endLine:4}).text,
    'func main() {\n  println("λ")\n}');
  for(const args of [{fileID:'/etc/passwd'},{fileID:'f',startLine:0},
    {fileID:'f',startLine:4,endLine:2},{fileID:'f',endLine:900}])
    assert.throws(()=>query.query(r,'source',args));
});

test('binding source retrieval includes retained supporting files without adding review membership',()=>{
  const r=fixture();
  r.supportingFiles=[{id:'generated',path:'wit_exports.go',source:'package main\n//go:wasmexport handle\nfunc wrapper() {}',sha256:'binding-sha'}];
  const result=query.query(r,'source',{fileID:'generated',startLine:2,endLine:3});
  assert.equal(result.text,'//go:wasmexport handle\nfunc wrapper() {}');
  assert.equal(result.sha256,'binding-sha');
  assert.equal(r.files.length,1);
  assert.equal(query.query(r,'overview',{}).entries.total,1);
});

test('flow rejects expansion of a leaf rather than reporting an unchanged diagram as expanded',()=>{
  const report=fixture(),context={entryID:'startup:main'};
  const leaf=query.query(report,'flow',context).nodes.find(n=>n.childIDs.length===0);
  assert.ok(leaf,'fixture must contain an existing leaf occurrence');
  assert.throws(()=>query.query(report,'flow',{...context,expandedIDs:[leaf.id]}),/no children/i);
});

test('search and evidence disclose scope and absent observations honestly',()=>{
  const r=fixture();
  const result=query.query(r,'search',{query:'rationale'});
  assert.equal(result.items[0].id,'main');
  assert.equal(result.total,1);
  assert.throws(()=>query.query(r,'search',{query:''}),/query/i);
  assert.throws(()=>query.query(r,'search',{query:'main',offset:-1}),/offset/i);
  const evidence=query.query(r,'evidence',{symbolID:'main'});
  assert.equal(evidence.unitEvidence,null);
  assert.equal(evidence.coverage,null);
  assert.equal(evidence.controlFlow.symbolID,'main');
  assert.equal(query.query(r,'packages',{}).items[0].path,'sample');
  assert.throws(()=>query.query(r,'document',{id:'missing'}),/document/i);
  assert.throws(()=>query.query(r,'evidence',{symbolID:'missing'}),/symbol/i);
});

test('architecture and optional runtime preserve source contracts and observation limits',()=>{
  const r=fixture();
  r.systemFlow.components=[{id:'app',name:'Application',kind:'component',package:'sample',members:['main'],sources:[]}];
  const context=query.query(r,'architecture',{level:'context'});
  assert.equal(context.nodes[0].id,'system');
  assert.equal(context.nodes[0].memberCount,1);
  assert.deepEqual(query.query(r,'architecture',{level:'context',section:'members',nodeID:'system'}).items,['app']);
  assert.throws(()=>query.query(r,'architecture',{componentID:'missing'}),/component/i);
  assert.throws(()=>query.query(r,'evidence',{runIndex:0}),/run/i);
  r.unitEvidence={sourceSHA256:'sha',runs:[{status:'pass',runtime:{
    status:'available',sourceSHA256:'sha',limitations:['Not a call trace.'],
    observations:[{kind:'launch',goroutine:'1',relatedGoroutine:'2'}]}}]};
  const runtime=query.query(r,'evidence',{runIndex:0});
  assert.equal(runtime.observations.items[0].kind,'launch');
  assert.equal(runtime.limitations.items[0],'Not a call trace.');
});

test('overview and run evidence disclose large diagnostics on demand, not in every response',()=>{
  const r=fixture(),diagnostic='diagnostic detail\n'.repeat(10000);
  r.assessment={version:'architecture-signals/1',sourceSHA256:'sha',
    packages:[],findings:[],checks:[{id:'tests',title:'Tests',status:'pass',detail:diagnostic,locations:[]}]};
  r.commands=[{executable:'go',args:['test'],exitCode:0,stdout:diagnostic,stderr:''}];
  r.unitEvidence={sourceSHA256:'sha',runs:[{status:'pass',commands:r.commands,matchedTests:['TestMain'],outcomes:{}}]};
  assert.ok(JSON.stringify(query.query(r,'overview',{})).length<10000,'overview flooded by diagnostics');
  assert.ok(JSON.stringify(query.query(r,'evidence',{})).length<10000,'run inventory flooded by diagnostics');
  for(const [args,key] of [[{checkID:'tests'},'detail'],[{commandIndex:0,section:'stdout'},'stdout'],
    [{runIndex:0,commandIndex:0,section:'stdout'},'stdout']]) {
    let text='',offset=0;
    do {
      const result=query.query(r,'evidence',{...args,textOffset:offset});
      assert.ok(Buffer.byteLength(JSON.stringify(result))<=32768);
      text+=result[key];offset=result.textPage?.nextOffset??null;
    }while(offset!==null);
    assert.equal(text,diagnostic);
  }
  assert.throws(()=>query.query(r,'evidence',{checkID:'unknown'}),/check/i);
  assert.throws(()=>query.query(r,'evidence',{commandIndex:99}),/command/i);
});

test('function evidence does not repeat every test outcome and run drill-down pages them',()=>{
  const r=fixture(),names=Array.from({length:10000},(_,i)=>'TestBehavior'+i);
  r.unitEvidence={sourceSHA256:'sha',runs:[{status:'pass',commands:[],matchedTests:names,
    outcomes:Object.fromEntries(names.map(name=>[name,'pass']))}]};
  const targeted=query.query(r,'evidence',{symbolID:'main',limit:1});
  assert.ok(JSON.stringify(targeted).length<10000,'function evidence repeats global test output');
  assert.equal(targeted.unitEvidence.runs.items[0].matchedTestCount,10000);
  assert.equal(targeted.unitEvidence.runs.items[0].outcomeCounts.pass,10000);
  const run=query.query(r,'evidence',{runIndex:0,limit:2,offset:2});
  assert.equal(run.testRun.matchedTests.total,10000);
  assert.deepEqual(run.testRun.matchedTests.items,['TestBehavior2','TestBehavior3']);
  assert.equal(run.testRun.outcomes.items[0].status,'pass');
});

test('package discovery has bounded gauges and explicit complete membership drill-down',()=>{
  const r=fixture();
  r.symbols=Array.from({length:4000},(_,i)=>({...r.symbols[0],id:'symbol-'+i,
    name:'Function'+i,cyclomatic:i===3999?43:2,doc:'Full rationale '+i}));
  r.packages[0].fileIDs=Array.from({length:2000},(_,i)=>'file-'+i);
  r.files=r.packages[0].fileIDs.map(id=>({id,path:id+'.go',package:'sample'}));
  r.packages[0].imports=Array.from({length:1000},(_,i)=>'dependency/'+i);
  const inventory=query.query(r,'packages',{limit:50}),p=inventory.items[0];
  assert.ok(JSON.stringify(inventory).length<4000,'package discovery must not embed membership');
  assert.equal(p.symbolCount,4000);
  assert.equal(p.fileCount,2000);
  assert.equal(p.importCount,1000);
  assert.equal(p.metrics.complexity.value,43);
  assert.deepEqual(p.metrics.complexity.maximumSymbolIDs,['symbol-3999']);
  assert.equal(p.metrics.symbolIDs,undefined);
  assert.equal(p.fileIDs,undefined);
  assert.equal(p.imports,undefined);
  const overview=query.query(r,'overview',{limit:2});
  assert.equal(overview.metrics.complexity.value,43,'module maximum must be discoverable without inventory scanning');
  let offset=0,ids=[];
  do {
    const result=query.query(r,'packages',{package:'sample',section:'symbols',limit:100,offset});
    assert.ok(JSON.stringify(result).length<=32768);
    ids.push(...result.items.map(s=>s.id));
    offset=result.nextOffset;
  }while(offset!==null);
  assert.deepEqual(ids,r.symbols.map(s=>s.id));
  offset=0;ids=[];
  do {
    const result=query.query(r,'packages',{package:'sample',section:'files',limit:100,offset});
    ids.push(...result.items.map(f=>f.id));offset=result.nextOffset;
  }while(offset!==null);
  assert.deepEqual(ids,r.packages[0].fileIDs);
  assert.equal(query.query(r,'evidence',{symbolID:'symbol-3999'}).symbol.cyclomatic,43);
  assert.deepEqual(query.query(r,'packages',{package:'sample',section:'imports',limit:2,offset:998}).items,
    ['dependency/998','dependency/999']);
});

test('metric ties are bounded and every maximum owner remains discoverable',()=>{
  const r=fixture();
  r.symbols=Array.from({length:500},(_,i)=>({...r.symbols[0],id:'tie-'+i}));
  const metrics=query.query(r,'packages',{}).items[0].metrics;
  assert.equal(metrics.complexity.maximumOwnerCount,500);
  assert.ok(metrics.complexity.maximumSymbolIDs.length<=10);
  const owners=query.query(r,'packages',{package:'sample',section:'maximumOwners',offset:490,limit:10});
  assert.deepEqual(owners.items,r.symbols.slice(490).map(s=>s.id));
  assert.equal(owners.nextOffset,null);
});

test('function discovery summarizes CFG and pages its nodes only when requested',()=>{
  const r=fixture(),cfg=r.controlFlows[0];
  cfg.nodes=Array.from({length:3000},(_,i)=>({id:'step-'+i,label:'step '+i,kind:'statement',location:{line:i+1}}));
  cfg.edges=cfg.nodes.slice(1).map((n,i)=>({from:cfg.nodes[i].id,to:n.id}));
  const result=query.query(r,'evidence',{symbolID:'main'});
  assert.ok(JSON.stringify(result).length<8000,'function metrics should not inline a full CFG');
  assert.equal(result.controlFlow.nodeCount,3000);
  assert.equal(result.controlFlow.nodes,undefined);
  assert.deepEqual(query.query(r,'evidence',{symbolID:'main',section:'controlFlowNodes',offset:2998,limit:2}).items,
    cfg.nodes.slice(2998));
  assert.deepEqual(query.query(r,'evidence',{symbolID:'main',section:'controlFlowEdges',offset:2997,limit:2}).items,
    cfg.edges.slice(2997));
});

test('architecture discovery summarizes hidden members and pages exact projection topology',()=>{
  const r=fixture();
  r.systemFlow.components=Array.from({length:1000},(_,i)=>({
    id:'component-'+i,name:'Component '+i,kind:'component',package:'sample',members:['main'],
    sources:[{fileID:'f',line:i+1}]}));
  const result=query.query(r,'architecture',{level:'context'});
  assert.ok(JSON.stringify(result).length<8000,'context discovery should not inline all component sources');
  assert.equal(result.nodes[0].memberCount,1000);
  assert.equal(result.nodes[0].members,undefined);
  assert.equal(result.nodes[0].sources,undefined);
  assert.equal(result.components,undefined,'component inventory is a separate explicit disclosure');
  assert.deepEqual(query.query(r,'architecture',{level:'context',section:'members',nodeID:'system',
    offset:998,limit:2}).items,['component-998','component-999']);
  assert.equal(query.query(r,'architecture',{section:'components',offset:998,limit:2}).items[0].id,'component-998');
});

test('pages bound bytes as well as item count, with no skipped results',()=>{
  const r=fixture();
  r.symbols=Array.from({length:100},(_,i)=>({...r.symbols[0],id:'s'+i,name:'main'+'x'.repeat(500)}));
  const result=query.query(r,'search',{query:'main',limit:100});
  assert.ok(JSON.stringify(result).length<=16384);
  assert.ok(result.items.length<100);
  assert.equal(result.nextOffset,result.items.length);
  const next=query.query(r,'search',{query:'main',limit:100,offset:result.nextOffset});
  assert.equal(next.items[0].id,r.symbols[result.nextOffset].id);
});

test('large explicit documents remain completely retrievable through bounded text pages',()=>{
  const r=fixture();
  r.documents[0].text='Complete rationale λ 🧭\n'.repeat(10000);
  let text='',offset=0;
  do {
    const result=query.query(r,'document',{id:'readme',textOffset:offset});
    assert.ok(Buffer.byteLength(JSON.stringify(result))<=32768);
    text+=result.text;
    offset=result.textPage.nextOffset;
  }while(offset!==null);
  assert.equal(text,r.documents[0].text);
});

test('all discovery surfaces reject oversized indivisible records rather than silently truncating',()=>{
  const r=fixture();
  r.symbols[0].name='main'+'x'.repeat(40000);
  assert.throws(()=>query.query(r,'search',{query:'main'}),/budget.*section|budget.*source/i);
  assert.throws(()=>query.query(fixture(),'packages',{package:'sample',section:'invented'}),/section/i);
  r.documents[0].path='x'.repeat(40000);
  assert.throws(()=>query.query(r,'document',{id:'readme'}),/32 KiB.*No evidence was truncated/);
});

test('overview pages large scope exclusions, initialization inventory and tied module maxima',()=>{
  const r=fixture();
  r.scope={root:'/snapshot',excludePaths:Array.from({length:2000},(_,i)=>'path/'+i),excludeTests:[]};
  r.symbols=Array.from({length:1000},(_,i)=>({...r.symbols[0],id:'init-'+i,name:'init'}));
  const result=query.query(r,'overview',{limit:100});
  assert.ok(Buffer.byteLength(JSON.stringify(result))<=32768);
  assert.equal(result.startup.length,0);
  const initialization=query.query(r,'overview',{section:'initialization',limit:100});
  assert.ok(initialization.items.length<1000);
  assert.ok(initialization.nextOffset>0);
  assert.equal(initialization.total,1000);
  assert.equal(result.scope.excludedPathCount,2000);
  assert.equal(result.scope.excludePaths,undefined);
  assert.equal(result.metrics.complexity.maximumOwnerCount,1000);
  assert.deepEqual(query.query(r,'overview',{section:'excludedPaths',offset:1998,limit:2}).items,['path/1998','path/1999']);
});

test('very large comments and source lines are fully accessible including JSON-escaped text',()=>{
  const r=fixture(),text=('\u0000\n"λ🧭').repeat(10000);
  r.symbols[0].comments=[{text,location:{fileID:'f',line:1}}];
  r.files[0].source=text;
  assert.equal(query.query(r,'evidence',{symbolID:'main'}).symbol.location.fileID,'f');
  assert.equal(query.query(r,'evidence',{symbolID:'main',section:'comments'}).items[0].text,undefined);
  for(const [operation,args,expected] of [
    ['evidence',{symbolID:'main',section:'comments',commentIndex:0},text],
    ['source',{fileID:'f',startLine:1,endLine:1},'\u0000']]) {
    let reconstructed='',offset=0;
    do {
      const result=query.query(r,operation,{...args,textOffset:offset});
      assert.ok(Buffer.byteLength(JSON.stringify(result))<=32768);
      reconstructed+=result.text;offset=result.textPage?.nextOffset??null;
    }while(offset!==null);
    assert.equal(reconstructed,expected);
  }
  assert.throws(()=>query.query(r,'evidence',{symbolID:'main',section:'comments',commentIndex:99}),/comment/i);
});

test('explicit flow and architecture topology pages reconstruct the exact UI projection',()=>{
  const r=fixture();
  for(const [operation,args,expected] of [
    ['flow',{entryID:'startup:main'},query.scene(r,{entryID:'startup:main'}).projection],
    ['architecture',{level:'context'},system.project(r,{level:'context'})]]) {
    for(const section of ['nodes','edges']) {
      let offset=0,items=[];
      do {
        const page=query.query(r,operation,{...args,section,limit:1,offset});
        items.push(...page.items);offset=page.nextOffset;
      }while(offset!==null);
      assert.deepEqual(items.map(n=>section==='nodes'?n.id:[n.from,n.to,n.kind]),
        expected[section].map(n=>section==='nodes'?n.id:[n.from,n.to,n.kind]));
    }
  }
});

test('architecture contracts, operation locations, component docs and alternative witnesses remain retrievable',()=>{
  const r=fixture(),location={fileID:'f',line:2};
  r.systemFlow.components=[{id:'app',name:'Application',kind:'component',package:'sample',
    doc:'Complete component rationale.',members:['main'],sources:[location]},
    {id:'db',name:'Database',kind:'durable-store',members:[],sources:[]}];
  r.systemFlow.operations=[{id:'op',name:'main',componentID:'app',symbolID:'main',location}];
  r.systemFlow.exchanges=[{id:'exchange',from:'app',to:'db',kind:'call',resolution:'static',
    information:[{name:'query',type:'string',direction:'input'}],sources:[location],callIDs:['call']}];
  r.systemFlow.alternatives=[{id:'alt',componentID:'app',symbolID:'main',decision:'if',label:'ok',
    location,execution:'possible',witnesses:[location],members:[location],requires:['ok']}];
  for(const [section,args,expected] of [
    ['componentMembers',{componentID:'app'},['main']],
    ['componentSources',{componentID:'app'},[location]],
    ['groupMembers',{group:'sample'},['app']],
    ['exchangeSources',{exchangeID:'exchange'},[location]],
    ['exchangeCalls',{exchangeID:'exchange'},['call']],
    ['exchangeInformation',{exchangeID:'exchange'},r.systemFlow.exchanges[0].information],
    ['alternativeMembers',{alternativeID:'alt'},[location]],
    ['alternativeWitnesses',{alternativeID:'alt'},[location]],
    ['alternativeRequires',{alternativeID:'alt'},['ok']]]) {
    assert.deepEqual(query.query(r,'architecture',{...args,section}).items,expected,section);
  }
  assert.equal(query.query(r,'architecture',{componentID:'app',section:'documentation'}).text,r.systemFlow.components[0].doc);
  assert.deepEqual(query.query(r,'architecture',{operationID:'op',section:'operation'}),r.systemFlow.operations[0]);
  assert.throws(()=>query.query(r,'evidence',{runIndex:0,section:'invented'}),/run/i);
});
