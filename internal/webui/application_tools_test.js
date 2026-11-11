const test=require('node:test');
const assert=require('node:assert/strict');
const application=require('./application-tools.js');
const query=require('./review-query-tools.js');

function snapshot(kind='wasm') {
  const location={fileID:'f',line:1};
  return {module:'sample',sourceSHA256:'sha',packages:[{path:'sample',name:'main'}],
    files:[{id:'f',path:'main.go',source:'package main'}],
    symbols:[{id:'main',kind:'function',name:'main',package:'sample',location},
      {id:'handler',kind:'function',name:'HandleMessage',package:'sample',location},
      {id:'init',kind:'function',name:'init',package:'sample',location}],
    controlFlows:[{symbolID:'handler',entry:'start',nodes:[
      {id:'start',kind:'entry',label:'start',callIDs:[],location},
      {id:'exit',kind:'exit',label:'exit',callIDs:[],location}],edges:[{from:'start',to:'exit'}]}],
    calls:[],httpEndpoints:[],documents:[],warnings:[],limitations:[],
    systemFlow:{sourceSHA256:'sha',components:[],operations:[],exchanges:[],alternatives:[]},
    entryPoints:{kind,initializers:['init'],limitations:[],sources:[
      {id:'wit',path:'wit/world.wit',sha256:'witsha',text:'world app { export handle: func(); }'}],
      entries:[{id:'wasm:handle',kind,name:'handle',symbolID:'handler',resolution:'resolved',
        world:'app',function:'handle',evidence:[{sourceID:'wit',line:1,kind:'wit-export'}]},
      {id:'wasm:unresolved',kind,name:'missing',resolution:'unresolved',reason:'No retained Go binding.',
        evidence:[{sourceID:'wit',line:1,kind:'wit-export'}]}]}};
}

test('shared entry inventory uses retained Wasm exports, not main or imported host interfaces',()=>{
  const r=snapshot(),rows=application.entryInventory(r);
  assert.deepEqual(rows.map(r=>r.id),['wasm:handle','wasm:unresolved']);
  assert.equal(rows[0].handlerID,'handler');
  assert.equal(application.entryByID(r,'startup:main'),undefined,'Wasm main must not masquerade as an external entry');
  assert.equal(application.initializationInventory(r)[0].handlerID,'init');
  assert.equal(application.entryInventory(r,{query:'missing'})[0].resolution,'unresolved');
});

test('MCP entry overview, source evidence and generic flows share exact retained identities',()=>{
  const r=snapshot(),overview=query.query(r,'overview',{});
  assert.equal(overview.projectKind,'wasm');
  assert.deepEqual(overview.entries.items.map(e=>e.id),application.entryInventory(r).map(e=>e.id));
  assert.equal(overview.startup.length,0,'initialization is a separate inventory');
  assert.equal(query.query(r,'overview',{section:'initialization'}).items[0].handlerID,'init');
  assert.equal(query.query(r,'overview',{section:'entrySources'}).items[0].id,'wit');
  assert.equal(query.query(r,'document',{id:'wit'}).text,r.entryPoints.sources[0].text);
  assert.ok(query.query(r,'search',{query:'world app'}).items.some(i=>i.kind==='document'&&i.id==='wit'));
  const flow=query.query(r,'flow',{entryID:'wasm:handle'});
  assert.equal(flow.entry.entryKind,'wasm');
  assert.ok(flow.nodes.some(n=>n.symbolID==='handler'));
  const unresolved=query.query(r,'flow',{entryID:'wasm:unresolved'});
  assert.ok(unresolved.limitations.items.some(l=>/No retained Go binding/.test(l)));
  assert.equal(query.query(r,'overview',{section:'entryEvidence',entryID:'wasm:handle'}).items[0].sourceID,'wit');
});

test('public library entry flows are not labeled HTTP and older snapshots request reanalysis honestly',()=>{
  const r=snapshot('library');
  const rows=application.entryInventory(r);
  assert.ok(rows.every(r=>r.entryKind==='library'));
  assert.ok(!JSON.stringify(query.query(r,'flow',{entryID:'wasm:handle'})).includes('HTTP handler completes'));
  delete r.entryPoints;
  assert.equal(application.entryInventory(r)[0].handlerID,'main');
  assert.equal(query.query(r,'overview',{}).entryDiscoveryStatus,'legacy');
});
