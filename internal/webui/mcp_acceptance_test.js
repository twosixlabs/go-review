const test=require('node:test');
const assert=require('node:assert/strict');
const {query}=require('./review-query-tools.js');

test('live MCP matches UI groups, scoped metrics, expansion, focus and retained evidence',
  {skip:!process.env.GO_REVIEW_MCP_URL},async()=>{
  const url=process.env.GO_REVIEW_MCP_URL;
  const response=await fetch(new URL('/api/snapshot',url));
  assert.ok(response.ok);
  const report=await response.json();
  let serial=0;
  async function rpc(method,params) {
    const r=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json',
      Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-06-18'},
      body:JSON.stringify({jsonrpc:'2.0',id:++serial,method,params})});
    assert.ok(r.ok,'MCP HTTP '+r.status);
    const result=await r.json();
    assert.equal(result.error,undefined,JSON.stringify(result.error));
    return result.result;
  }
  await rpc('initialize',{protocolVersion:'2025-06-18',capabilities:{},
    clientInfo:{name:'ui-parity',version:'1'}});
  const overview=await rpc('tools/call',{name:'review_overview',arguments:{module:report.module}});
  assert.ok(!overview.isError,JSON.stringify(overview));
  const snapshotID=overview.structuredContent.snapshot.id;
  assert.ok(Buffer.byteLength(JSON.stringify(overview.structuredContent))<=32768,'overview violates byte budget');
  assert.equal(overview.structuredContent.data.metrics.complexity.value,
    Math.max(0,...report.symbols.map(s=>s.cyclomatic||0)));
  assert.equal(overview.structuredContent.snapshot.sourceSHA256,report.sourceSHA256);
  async function call(name,args) {
    const r=await rpc('tools/call',{name,arguments:{snapshotID,...args}});
    assert.ok(!r.isError,JSON.stringify(r));
    assert.equal(r.structuredContent.snapshot.id,snapshotID);
    assert.ok(Buffer.byteLength(JSON.stringify(r.structuredContent))<=32768,name+' violates byte budget');
    return r.structuredContent.data;
  }
  const plain=value=>JSON.parse(JSON.stringify(value));
  const entries=report.module==='builder'?[
    'POST /api/missions/{missionID}/builds','POST /api/executions',
    'POST /api/gitlab/workflows','GET /api/missions/{missionID}'
  ].map(pattern=>{
    const entry=report.httpEndpoints.find(e=>e.pattern===pattern);
    assert.ok(entry,'missing acceptance route '+pattern);return entry;
  }):report.httpEndpoints.slice(0,4);
  for(const entry of entries) {
    const context={entryID:entry.id},expected=query(report,'flow',context);
    const flow=await call('review_flow',context);
    assert.deepEqual(flow,plain(expected));
    const expandable=flow.nodes.find(n=>n.childIDs.length);
    if(expandable) {
      const expanded={...context,expandedIDs:[expandable.id]};
      assert.deepEqual(await call('review_flow',expanded),plain(query(report,'flow',expanded)));
      const focused={...context,focusSymbolID:expandable.symbolID};
      assert.deepEqual(await call('review_flow',focused),plain(query(report,'flow',focused)));
      assert.deepEqual(await call('review_inspect',{...context,nodeID:expandable.id}),
        plain(query(report,'inspect',{...context,nodeID:expandable.id})));
      const ownerID=expandable.metrics.complexity.maximumSymbolIDs[0];
      if(ownerID) {
        const evidence=await call('review_evidence',{symbolID:ownerID,limit:2});
        assert.equal(evidence.symbol.cyclomatic,expandable.metrics.complexity.value);
        assert.equal(evidence.metrics.memberCount,1);
        assert.ok(Buffer.byteLength(JSON.stringify(evidence))<=32768,'function inspection repeats global run output');
        assert.equal(evidence.controlFlow?.nodes,undefined);
      }
    }
    if(flow.edges.length) {
      const edge=flow.edges[0],args={...context,from:edge.from,to:edge.to,kind:edge.kind};
      assert.deepEqual(await call('review_inspect',args),plain(query(report,'inspect',args)));
    }
  }
  const document=report.documents[0];
  for(const args of [{level:'context'},{level:'components'}])
    assert.deepEqual(await call('review_architecture',args),plain(query(report,'architecture',args)));
  async function text(name,args,key) {
    let value='',offset=0;
    do {
      const data=await call(name,{...args,textOffset:offset});
      value+=data[key];offset=data.textPage?.nextOffset??null;
    }while(offset!==null);
    return value;
  }
  if(document)assert.equal(await text('review_document',{id:document.id},'text'),document.text);
  const file=report.files[0],lines=file.source.split('\n').length;
  const source={fileID:file.id,startLine:1,endLine:Math.min(lines,20)};
  assert.deepEqual(await call('review_source',source),plain(query(report,'source',source)));
  if(report.assessment?.checks?.length) {
    const check=report.assessment.checks[0];
    assert.equal(await text('review_evidence',{checkID:check.id},'detail'),check.detail);
    assert.deepEqual((await call('review_evidence',{checkID:check.id,section:'locations'})).items,
      (check.locations||[]).slice(0,10));
  }
  if(report.commands?.length) {
    assert.equal((await call('review_evidence',{commandIndex:0})).stdout,undefined);
    for(const section of ['stdout','stderr'])
      assert.equal(await text('review_evidence',{commandIndex:0,section},section),report.commands[0][section]||'');
  }
  for(const limit of [5,50]) {
    const packages=await call('review_packages',{limit});
    assert.equal(packages.items[0]?.metrics.symbolIDs,undefined);
    assert.equal(packages.items[0]?.fileIDs,undefined);
    assert.equal(packages.items[0]?.imports,undefined);
  }
  if(report.unitEvidence?.runs?.length) {
    const run=await call('review_evidence',{runIndex:0,limit:2});
    assert.equal(run.testRun.matchedTests.total,report.unitEvidence.runs[0].matchedTests.length);
    assert.ok(run.testRun.matchedTests.items.length<=2);
  }
  const stale=await rpc('tools/call',{name:'review_document',arguments:{snapshotID:'stale',id:'unknown'}});
  assert.equal(stale.isError,true);
});
