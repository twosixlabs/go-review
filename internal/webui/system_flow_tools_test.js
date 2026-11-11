const test=require('node:test');
const assert=require('node:assert/strict');
const tools=require('./system-flow-tools.js');

function fixture() {
  return {module:'app',sourceSHA256:'source',systemFlow:{
    sourceSHA256:'source',components:[
      {id:'api',name:'API',kind:'component',package:'app/api',members:['handle'],sources:[]},
      {id:'service',name:'Service',kind:'component',package:'app/service',members:['create'],sources:[]},
      {id:'store',name:'Records',kind:'memory-store',package:'app/storage',members:['save'],sources:[]},
      {id:'actor',name:'HTTP clients',kind:'actor',members:[],sources:[]},
      {id:'remote',name:'GitClient',kind:'contract',package:'example.com/git',members:[],sources:[]}
    ],operations:[
      {id:'create-key',name:'Create',componentID:'service',symbolID:'create',location:{fileID:'f',line:4}}
    ],exchanges:[
      {id:'entry',from:'actor',to:'api',kind:'ingress',resolution:'resolved',information:[{type:'HTTP request',direction:'input'},{type:'HTTP response',direction:'output'}],sources:[],callIDs:[]},
      {id:'create-exchange',from:'api',to:'service',kind:'exchange',resolution:'resolved',information:[{type:'app.Request',direction:'input'},{type:'app.Receipt',direction:'output'}],sources:[],callIDs:['call-create']},
      {id:'save-exchange',from:'service',to:'store',kind:'exchange',resolution:'inferred',information:[{type:'app.Record',direction:'input'},{type:'error',direction:'output'}],sources:[],callIDs:['call-save']},
      {id:'remote-exchange',from:'service',to:'remote',kind:'asynchronous',resolution:'unresolved',information:[{type:'app.Event',direction:'input'},{type:'error',direction:'output'}],sources:[],callIDs:['call-remote']}
    ],alternatives:[
      {id:'yes',componentID:'service',symbolID:'create',decision:'valid',label:'true',execution:'exercised',members:[],witnesses:[],requires:[]},
      {id:'no',componentID:'service',symbolID:'create',decision:'valid',label:'false',execution:'not-observed',members:[],witnesses:[],requires:[]},
      {id:'unknown',componentID:'service',symbolID:'create',decision:'stored',label:'failure',execution:'unknown',members:[],witnesses:[],requires:[]}
    ],limitations:['Typed contracts are not object lineage.']
  },operationSources:{calls:[{callID:'call-save',callerKey:'create-key'},{callID:'call-remote',callerKey:'create-key'}]}};
}

test('architectural exchanges reverse output contracts without fabricating synchronous async returns',()=>{
  const scene=tools.project(fixture(),{level:'operation',componentID:'service',operationID:'create-key'});
  const input=scene.edges.find(e=>e.from==='service'&&e.to==='store');
  const output=scene.edges.find(e=>e.from==='store'&&e.to==='service');
  assert.ok(input.information.some(i=>i.type==='app.Record'));
  assert.ok(output.information.some(i=>i.type==='error'));
  assert.equal(scene.edges.some(e=>e.from==='remote'&&e.to==='service'),false);
  assert.equal(scene.alternatives.length,3);
  assert.equal(tools.executionState(scene.alternatives),'mixed');
  assert.deepEqual(scene.alternatives.map(a=>a.execution),['exercised','not-observed','unknown']);
});

test('context groups implementation instead of drawing each production function',()=>{
  const report=fixture(),before=JSON.stringify(report);
  const scene=tools.project(report,{level:'context'});
  assert.ok(scene.nodes.some(n=>n.id==='system'&&n.members.includes('service')));
  assert.equal(scene.nodes.some(n=>n.id==='create-key'),false);
  const represented=new Set(scene.nodes.flatMap(n=>n.members));
  for(const component of report.systemFlow.components)assert.ok(represented.has(component.id),component.id);
  assert.equal(JSON.stringify(report),before);
});

test('component groups keep unknown boundaries and every member discoverable',()=>{
  const scene=tools.project(fixture(),{level:'components',group:'app/service'});
  assert.ok(scene.nodes.some(n=>n.id==='service'));
  assert.ok(scene.edges.some(e=>e.resolution==='unresolved'));
  assert.equal(scene.nodes.some(n=>n.kind==='test'||n.kind==='mock'),false);
});

test('mismatched and missing architectural facts are explicit, not substituted with call graphs',()=>{
  const report=fixture();
  report.systemFlow.sourceSHA256='old';
  const stale=tools.project(report,{level:'context'});
  assert.equal(stale.nodes.length,0);
  assert.match(stale.error,/snapshot/i);
  delete report.systemFlow;
  assert.match(tools.project(report,{}).error,/source|architectur/i);
});

test('selected component keeps its typed neighbourhood while preserving catalog search',()=>{
  const scene=tools.project(fixture(),{level:'operation',componentID:'service',query:'service'});
  assert.ok(scene.edges.some(e=>e.information.some(i=>i.type==='app.Record')));
  assert.ok(scene.nodes.some(n=>n.members.includes('remote')));
});
test('component neighbourhood omits unrelated boundaries without a package',()=>{
  const report=fixture();
  report.systemFlow.components.push({id:'unrelated',name:'Unresolved callback',kind:'contract',members:[],sources:[]});
  const scene=tools.project(report,{level:'operation',componentID:'service',operationID:'create-key'});
  assert.equal(scene.nodes.some(n=>n.members.includes('unrelated')||n.members.includes('actor')),false);
  assert.ok(scene.nodes.some(n=>n.members.includes('remote')));
});

test('standard in-process utilities collapse behind one expandable supporting boundary',()=>{
  const report=fixture();
  for(const name of ['context','sync']) {
    report.systemFlow.components.push({id:name,name,kind:'contract',package:name,members:[],sources:[]});
    report.systemFlow.exchanges.push({id:name,from:'service',to:name,kind:'exchange',resolution:'unresolved',information:[],callIDs:[]});
  }
  const scene=tools.project(report,{level:'operation',componentID:'service'});
  const support=scene.nodes.find(n=>n.name==='Supporting libraries');
  assert.ok(support);
  assert.deepEqual(support.members.sort(),['context','sync']);
  assert.equal(scene.exchanges.filter(e=>['context','sync'].includes(e.id)).length,2);
});

test('context consolidates dependency kinds and parallel invocation forms without losing contracts',()=>{
  const report=fixture();
  report.systemFlow.components.push({id:'library',name:'Library',kind:'external',package:'other.test/lib',members:[],sources:[]});
  report.systemFlow.exchanges.push({id:'lib',from:'api',to:'library',kind:'exchange',resolution:'unresolved',
    information:[{type:'app.Options',direction:'input'}],sources:[],callIDs:[]});
  const scene=tools.project(report,{level:'context'});
  assert.equal(scene.nodes.length,3);
  const dependency=scene.nodes.find(n=>n.members.includes('remote'));
  assert.ok(dependency.members.includes('library'));
  assert.equal(scene.edges.filter(e=>e.from==='system'&&e.to===dependency.id).length,1);
  const edge=scene.edges.find(e=>e.from==='system'&&e.to===dependency.id);
  assert.ok(edge.information.some(i=>i.type==='app.Options'));
  assert.deepEqual(edge.kinds,['asynchronous','exchange']);
  assert.deepEqual(edge.exchangeIDs,['remote-exchange','lib']);
  assert.equal(scene.edges.some(e=>e.from===dependency.id&&e.to==='system'),false);
});
