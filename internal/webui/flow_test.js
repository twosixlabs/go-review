const test = require('node:test');
const assert = require('node:assert/strict');
const {ranks, coverageFor, compactFlow} = require('./flow.js');

function statementFlow() {
  const nodes=['entry','a','b','c','call','d','e','return'].map((id,i)=>({
    id,kind:id==='entry'?'entry':id==='return'?'return':'statement',label:id,
    statementBlock:['a','b','c','d','e'].includes(id)?'block:0':undefined,
    reachable:true,callIDs:id==='call'?['call-1']:[],comments:[],
    location:{fileID:'f',line:i+1,column:1,endLine:i+1,endColumn:8,offset:i*10,endOffset:i*10+7}
  }));
  return {entry:'entry',nodes,edges:nodes.slice(1).map((n,i)=>({from:nodes[i].id,to:n.id,kind:'sequence',label:''}))};
}

test('compact flow groups simple runs but leaves calls and returns visible without changing evidence',()=>{
  const flow=statementFlow(),before=JSON.stringify(flow);
  const result=compactFlow(flow);
  assert.deepEqual(result.nodes.map(n=>n.members?n.members.map(m=>m.id):n.id),['entry',['a','b','c'],'call',['d','e'],'return']);
  const group=result.nodes[1];
  assert.equal(group.kind,'statements');
  assert.equal(group.label,'a\nb\nc');
  assert.deepEqual(group.locations,flow.nodes.slice(1,4).map(n=>n.location));
  assert.equal(group.location.line,2);assert.equal(group.location.endLine,4);
  assert.deepEqual(result.edges.map(e=>[e.from,e.to]),[
    ['entry',group.id],[group.id,'call'],['call',result.nodes[3].id],[result.nodes[3].id,'return']
  ]);
  assert.equal(JSON.stringify(flow),before);
});
test('expansion restores original nodes and edges with stable group identities',()=>{
  const flow=statementFlow(),group=compactFlow(flow).nodes[1];
  const expanded=compactFlow(flow,new Set([group.id]));
  assert.deepEqual(expanded.nodes.slice(1,4).map(n=>n.id),['a','b','c']);
  assert.deepEqual(expanded.edges.slice(0,4),flow.edges.slice(0,4));
  assert.equal(expanded.groups.find(g=>g.id===group.id).members.length,3);
  assert.equal(compactFlow(flow).nodes[1].id,group.id);
});
test('compact flow retains original self loops and all labeled boundary edges',()=>{
  const flow=statementFlow();
  flow.edges.push({from:'call',to:'call',kind:'loop',label:'again'});
  flow.edges.push({from:'return',to:'a',kind:'branch',label:'retry'});
  const result=compactFlow(flow);
  assert.ok(result.edges.some(e=>e.from==='call'&&e.to==='call'&&e.label==='again'));
  assert.ok(result.edges.some(e=>e.from==='return'&&e.label==='retry'));
});
test('compact flow never crosses CFG blocks, decisions, joins, backedges or reachability boundaries',()=>{
  for(const boundary of ['block','branch','join','loop','reachability','call','file','backward']) {
    const flow=statementFlow();
    if(boundary==='block')flow.nodes[2].statementBlock='block:1';
    if(boundary==='branch')flow.edges.push({from:'a',to:'return',kind:'branch',label:'false'});
    if(boundary==='join')flow.edges.push({from:'entry',to:'b',kind:'sequence',label:''});
    if(boundary==='loop')flow.edges[1].kind='loop';
    if(boundary==='reachability')flow.nodes[2].reachable=false;
    if(boundary==='call')flow.nodes[2].callIDs=['call'];
    if(boundary==='file')flow.nodes[2].location.fileID='other';
    if(boundary==='backward')flow.nodes[2].location.offset=0;
    const result=compactFlow(flow);
    assert.ok(!result.nodes.some(n=>n.members?.some(m=>m.id==='a')&&n.members?.some(m=>m.id==='b')),boundary);
    assert.equal(result.nodes.flatMap(n=>n.members||[n]).length,flow.nodes.length,boundary);
  }
});
test('group coverage aggregates member ranges only and retains unique comments',()=>{
  const flow=statementFlow(),comment={text:'intent',location:{fileID:'f',offset:10,endOffset:15}};
  flow.nodes[1].comments=[comment];flow.nodes[2].comments=[comment];
  const group=compactFlow(flow).nodes[1];
  assert.deepEqual(group.comments,[comment]);
  const profile={blocks:[
    {fileID:'f',startLine:2,startColumn:1,endLine:2,endColumn:7,count:1,statements:3},
    {fileID:'f',startLine:3,startColumn:1,endLine:3,endColumn:7,count:0,statements:1},
    {fileID:'f',startLine:9,startColumn:1,endLine:9,endColumn:7,count:0,statements:100}
  ]};
  assert.equal(coverageFor(group.locations,profile).state,'mixed');
  assert.equal(coverageFor(group.locations,profile).blocks.length,2);
});

test('branch and loop layout keeps entry before successors without chasing cycles', () => {
  const nodes = ['entry','condition','body','exit'].map(id => ({id}));
  const positions = ranks(nodes, [
    {from:'entry',to:'condition'}, {from:'condition',to:'body'},
    {from:'condition',to:'exit'}, {from:'body',to:'condition'}
  ], 'entry');
  assert.equal(positions.get('entry').rank, 0);
  assert.equal(positions.get('condition').rank, 1);
  assert.equal(positions.get('body').rank, 2);
  assert.equal(positions.get('exit').rank, 2);
});

test('coverage distinguishes no matching profile blocks from uncovered source', () => {
  const location = {fileID:'f',line:10,column:2,endLine:10,endColumn:20};
  assert.equal(coverageFor(location,null).state,'unknown');
  const coverage = {blocks:[
    {fileID:'f',startLine:10,startColumn:2,endLine:10,endColumn:8,count:0,statements:1},
    {fileID:'f',startLine:10,startColumn:8,endLine:10,endColumn:20,count:3,statements:1}
  ]};
  assert.equal(coverageFor(location,coverage).state,'mixed');
  assert.equal(coverageFor({...location,line:20,endLine:20},coverage).state,'unknown');
  assert.equal(coverageFor({...location,endColumn:8},coverage).state,'uncovered');
});
