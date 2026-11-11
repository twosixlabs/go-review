const test=require('node:test');
const assert=require('node:assert/strict');

function facts() {
  const loc=(line,endLine=line+1)=>({fileID:'f',line,column:1,endLine,endColumn:1});
  const node=(id,symbolID,activityID,phase='operation')=>({id,symbolID,activityID,phase,
    label:id,information:[],alternatives:[],location:loc(1)});
  return {
    report:{sourceSHA256:'sha',symbols:[
      {id:'entry',name:'Serve',package:'sample',kind:'function',location:loc(1),cyclomatic:2},
      {id:'work',name:'Process',doc:'Process validates and stores the request.\n\nIt preserves the original identifier and rejects invalid input.',
        package:'sample',kind:'method',location:loc(3),cyclomatic:17},
      {id:'save',name:'Save',package:'sample',kind:'method',location:loc(5),cyclomatic:4}
    ],files:[{id:'f',path:'sample.go',package:'sample'}],coverage:{blocks:[
      {fileID:'f',startLine:3,startColumn:1,endLine:4,endColumn:1,statements:3,count:2},
      {fileID:'f',startLine:5,startColumn:1,endLine:6,endColumn:1,statements:7,count:0}
    ]},assessment:{version:'architecture-signals/1',sourceSHA256:'sha',
      packages:[{package:'sample',level:'concern'}],checks:[],
      findings:[{symbolID:'save',package:'sample',severity:'high',location:loc(5)}]}},
    scene:{entry:'e',nodes:[node('e','entry'),node('a','work','e'),node('b','save','a'),
      node('c','save','a'),node('unknown',undefined,'a'),node('bg','save','a','async-work'),
      {...node('done',undefined,undefined,'completion'),label:'Operation completes'}],
      edges:[{from:'e',to:'a'},{from:'a',to:'b'},{from:'b',to:'c'},
        {from:'c',to:'unknown'},{from:'unknown',to:'done',guards:['valid · true']},
        {from:'c',to:'bg',kind:'asynchronous'}],limitations:[]}
  };
}

test('compound projection preserves occurrences, independent background work and terminal children',()=>{
  const tools=require('./visual-review-tools.js'),{report,scene}=facts(),before=JSON.stringify(scene);
  const tree=tools.hierarchy(report,scene),closed=tools.project(tree,[]),open=tools.project(tree,['a']);
  assert.deepEqual(closed.nodes.map(n=>n.id),['e','a','bg','done']);
  assert.deepEqual(tree.byID.get('a').children,['b','c','unknown']);
  assert.deepEqual(tree.byID.get('a').symbolIDs,['work','save']);
  assert.deepEqual(open.nodes.map(n=>n.id),['e','a','b','c','unknown','bg','done']);
  assert.equal(open.nodes.find(n=>n.id==='b').parentID,'a');
  assert.ok(closed.edges.some(e=>e.from==='a'&&e.to==='done'&&e.guards.includes('valid · true')));
  assert.ok(closed.edges.some(e=>e.from==='a'&&e.to==='bg'&&e.kind==='asynchronous'));
  assert.ok(!closed.edges.some(e=>e.from==='bg'));
  assert.deepEqual(new Set(closed.sourceIDs),new Set(['e','a','b','c','unknown','bg','done']));
  assert.equal(JSON.stringify(scene),before);
  assert.equal(tree.byID.get('a').description,report.symbols[1].doc);
});

test('compound gauges deduplicate coverage and use worst concern without hiding unknown members',()=>{
  const tools=require('./visual-review-tools.js'),{report}=facts();
  const m=tools.metrics(report,['work','save','save','missing']);
  assert.equal(m.coverage.total,10);
  assert.equal(m.coverage.covered,3);
  assert.equal(m.coverage.value,30);
  assert.equal(m.complexity.value,17);
  assert.equal(m.complexity.aggregation,'maximum');
  assert.deepEqual(m.complexity.maximumSymbolIDs,['work']);
  assert.equal(m.scope,'represented-unique-operation-definitions');
  assert.deepEqual(m.symbolIDs,['work','save','missing']);
  assert.equal(m.architecture.level,'high-concern');
  assert.equal(m.architecture.unknown,1);
  assert.equal(m.complexity.unknown,1);
  assert.equal(m.memberCount,3);
  assert.equal(tools.metrics({...report,coverage:null,assessment:null},['work']).coverage.value,null);
  assert.equal(tools.metrics({...report,assessment:null},['work']).architecture.level,'not-assessed');
  assert.equal(tools.metrics(report,[]).complexity.value,null);
});

test('projection refuses invalid source ownership rather than discarding nodes',()=>{
  const tools=require('./visual-review-tools.js'),{report,scene}=facts();
  scene.nodes.find(n=>n.id==='a').activityID='b';
  assert.match(tools.hierarchy(report,scene).error,/cycle/i);
  assert.match(tools.hierarchy(report,{error:'Snapshot does not match'}).error,/does not match/);
});

test('flow layout aligns the longest source continuation and puts early exits beside it',()=>{
  const {layout}=require('./visual-review-tools.js');
  const nodes=['entry','decode','work','open','project','respond','done','reject','failed'].map(id=>({
    id,parentID:null,phase:id==='entry'?'entry':id==='done'?'completion':'operation'}));
  const edge=(from,to,kind='control')=>({from,to,kind});
  const scene={entry:'entry',nodes,edges:[
    edge('entry','decode'),edge('decode','reject'),edge('reject','done'),
    edge('decode','work'),edge('work','failed'),edge('failed','done'),
    edge('work','open'),edge('open','project'),edge('project','respond'),edge('respond','done')
  ]},before=JSON.stringify(scene);
  const result=layout(scene,nodes.map(n=>n.id));
  assert.deepEqual(result.spine,['entry','decode','work','open','project','respond','done']);
  const positions=new Map(result.placements.map(p=>[p.id,p]));
  assert.deepEqual(result.spine.map(id=>positions.get(id).column),[1,1,1,1,1,1,1]);
  assert.deepEqual(result.spine.map(id=>positions.get(id).row),[1,2,3,4,5,6,7]);
  assert.equal(positions.get('reject').column,2);
  assert.ok(positions.get('reject').row>=positions.get('decode').row);
  assert.equal(JSON.stringify(scene),before);
});

test('layout preserves loops, ambiguous branches, nested ownership and independent async work',()=>{
  const {layout}=require('./visual-review-tools.js');
  const nodes=[
    {id:'entry',phase:'entry'},{id:'a'},{id:'b'},{id:'done',phase:'completion'},
    {id:'nested',parentID:'a'},{id:'bg',phase:'async-work'}
  ];
  const edges=[['entry','a'],['a','b'],['b','a'],['b','done'],['a','nested'],['nested','b']]
    .map(([from,to])=>({from,to,kind:'control'}));
  edges.push({from:'a',to:'bg',kind:'asynchronous'});
  const scene={entry:'entry',nodes,edges},ids=['entry','a','b','done','bg'];
  const result=layout(scene,ids);
  assert.deepEqual(result.spine,['entry','a','b','done']);
  assert.deepEqual(result.background,['bg']);
  assert.equal(result.placements.length,4);
  assert.deepEqual(layout(scene,['nested']).spine,['nested']);
  const diamond={entry:'entry',nodes:nodes.filter(n=>!['nested','bg'].includes(n.id)),
    edges:[['entry','a'],['entry','b'],['a','done'],['b','done']].map(([from,to])=>({from,to}))};
  assert.equal(layout(diamond,['entry','a','b','done']).ambiguous,true);
});
