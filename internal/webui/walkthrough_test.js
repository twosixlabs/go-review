const test=require('node:test');
const assert=require('node:assert/strict');
const {endpointWalkthrough,walkthroughPositions}=require('./walkthrough-tools.js');

function fixture() {
  const loc=offset=>({fileID:'f',offset,endOffset:offset+5,line:offset,column:1,endLine:offset,endColumn:6});
  const node=(id,kind,callIDs=[],offset=1)=>({id,kind,callIDs,label:id,reachable:true,location:loc(offset),comments:[]});
  const edge=(from,to,label='',kind='sequence')=>({from,to,label,kind});
  return {
    symbols:[{id:'handler',name:'handle',kind:'function',package:'m/http'},
      {id:'helper',name:'decode',kind:'function',package:'m/http'},
      {id:'service',name:'Check',kind:'method',package:'m/app'}],
    calls:[
      {id:'first',from:'handler',to:'helper',kind:'static',invocation:'synchronous',location:loc(2)},
      {id:'second',from:'handler',to:'service',kind:'static',invocation:'synchronous',location:loc(4)},
      {id:'repeat',from:'handler',to:'service',kind:'static',invocation:'synchronous',location:loc(6)},
      {id:'defer',from:'handler',to:'service',kind:'static',invocation:'deferred',location:loc(7)},
      {id:'go',from:'handler',to:'service',kind:'static',invocation:'goroutine',location:loc(8)}
    ],
    controlFlows:[
      {symbolID:'handler',entry:'entry',limitations:['Fixture expression order is not expanded.'],nodes:[
        node('entry','entry'),node('decode','statement',['first'],2),node('condition','expression',[],3),
        node('check','statement',['second'],4),node('early','return',[],5),
        node('again','statement',['repeat'],6),node('defer','defer',['defer'],7),
        node('launch','go',['go'],8),node('exit','exit',[],9),node('dead','statement',[],10)],
      edges:[edge('entry','decode'),edge('decode','condition'),edge('condition','check','true','branch'),
        edge('condition','early','false','branch'),edge('check','again'),edge('early','exit','return','return'),
        edge('again','defer'),edge('defer','launch'),edge('launch','exit')]},
      ...['helper','service'].map(id=>({symbolID:id,entry:'entry',limitations:[],nodes:[
        node('entry','entry'),node('ret','return',[],2),node('panic','panic',[],3),node('exit','exit',[],4)],
      edges:[edge('entry','ret'),edge('ret','exit','return','return'),edge('panic','exit','panic','panic')]}))
    ]
  };
}
const endpoint={id:'route',handlerID:'handler',pattern:'POST /check',patternKnown:true};
const step=(scene,raw)=>scene.nodes.find(n=>n.ownerID==='handler'&&n.step?.id===raw);
test('walkthrough follows CFG call sites, not package order or one card per target',()=>{
  const r=fixture();r.controlFlows[0].nodes.find(n=>n.id==='dead').reachable=false;
  const before=JSON.stringify(r),scene=endpointWalkthrough(r,endpoint);
  const first=step(scene,'decode'),check=step(scene,'check'),again=step(scene,'again');
  assert.equal(first.calls[0].to,'helper');
  assert.equal(check.calls[0].to,again.calls[0].to);
  assert.notEqual(check.id,again.id);
  assert.ok(scene.edges.some(e=>e.from===first.id&&e.to===step(scene,'condition').id));
  assert.ok(scene.edges.some(e=>e.from===step(scene,'condition').id&&e.to===step(scene,'early').id&&e.label==='false'));
  assert.equal(scene.nodes.some(n=>n.step?.id==='dead'),false);
  assert.equal(JSON.stringify(r),before);
});
test('expanding a synchronous occurrence inserts callee and returns to that callers continuation',()=>{
  const r=fixture(),base=endpointWalkthrough(r,endpoint),check=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[check.id+'::second']});
  const callEdge=scene.edges.find(e=>e.from===check.id&&e.kind==='call');
  assert.ok(callEdge);
  const child=scene.nodes.find(n=>n.id===callEdge.to);
  assert.equal(child.ownerID,'service');
  assert.ok(scene.edges.some(e=>e.to===step(scene,'again').id&&e.kind==='return-to-caller'));
  assert.equal(scene.edges.some(e=>e.from===check.id&&e.to===step(scene,'again').id),false);
  assert.equal(scene.nodes.filter(n=>n.ownerID==='service'&&n.step?.id==='entry').length,1);
  assert.equal(step(scene,'again').calls[0].id,'repeat');
  assert.equal(scene.edges.some(e=>e.kind==='panic'&&e.to===scene.nodes.find(n=>n.ownerID==='service'&&n.step?.id==='exit').id),false);
});
test('defer and goroutine exploration never become synchronous call-and-return paths',()=>{
  const r=fixture(),base=endpointWalkthrough(r,endpoint);
  for(const [raw,call] of [['defer','defer'],['launch','go']]) {
    const site=step(base,raw),scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::'+call]});
    assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='inspection'&&e.indirect));
    assert.ok(scene.edges.some(e=>e.from===site.id&&e.to===step(scene,raw==='defer'?'launch':'exit').id&&e.kind!=='inspection'));
    assert.equal(scene.edges.some(e=>e.kind==='return-to-caller'),false);
    assert.equal(step(scene,raw).kind,raw==='defer'?'defer':'go');
  }
});
test('multi-call expressions stay one step with explicit ordering boundary and all source call sites',()=>{
  const r=fixture();r.controlFlows[0].nodes.find(n=>n.id==='check').callIDs=['second','first'];
  const base=endpointWalkthrough(r,endpoint),site=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::second']});
  assert.equal(step(scene,'check').calls.length,2);
  assert.ok(step(scene,'check').orderBoundary);
  assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='inspection'));
  assert.equal(scene.edges.some(e=>e.kind==='return-to-caller'),false);
});
test('known nested-call order puts arguments before the outer invocation and permits synchronous drill-in',()=>{
  const r=fixture(),flow=r.controlFlows[0],source=flow.nodes.find(n=>n.id==='check');
  source.callIDs=['second','first'];source.callOrderKnown=true;
  r.calls.find(c=>c.id==='first').evaluationOrder=1;
  r.calls.find(c=>c.id==='second').evaluationOrder=2;
  const base=endpointWalkthrough(r,endpoint);
  const argument=base.nodes.find(n=>n.calls?.[0]?.id==='first'&&n.step?.id==='check');
  const outer=base.nodes.find(n=>n.calls?.[0]?.id==='second');
  assert.ok(base.edges.some(e=>e.from===argument.id&&e.to===outer.id));
  assert.equal(outer.orderBoundary,false);
  const scene=endpointWalkthrough(r,endpoint,{expanded:[outer.id+'::second']});
  assert.ok(scene.edges.some(e=>e.from===outer.id&&e.kind==='call'));
  assert.ok(scene.edges.some(e=>e.kind==='return-to-caller'));
});
test('a synchronous argument to deferred work is not itself labeled or expanded as deferred',()=>{
  const r=fixture(),source=r.controlFlows[0].nodes.find(n=>n.id==='defer');
  source.callIDs=['defer','first'];source.callOrderKnown=true;
  r.calls.find(c=>c.id==='first').evaluationOrder=1;
  r.calls.find(c=>c.id==='defer').evaluationOrder=2;
  const base=endpointWalkthrough(r,endpoint),argument=base.nodes.find(n=>n.calls?.[0]?.id==='first'&&n.step?.id==='defer');
  assert.doesNotMatch(argument.kind,/defer/);
  const scene=endpointWalkthrough(r,endpoint,{expanded:[argument.id+'::first']});
  assert.ok(scene.edges.some(e=>e.from===argument.id&&e.kind==='call'));
});
test('loops retain backedges and layout puts the join after both alternatives',()=>{
  const r=fixture(),flow=r.controlFlows[0];
  flow.edges.push({from:'again',to:'condition',label:'loop',kind:'loop'});
  const scene=endpointWalkthrough(r,endpoint),positions=walkthroughPositions(scene);
  assert.equal(positions.size,scene.nodes.length);
  assert.ok(scene.edges.some(e=>e.label==='loop'));
  assert.ok(positions.get(step(scene,'exit').id).rank>positions.get(step(scene,'early').id).rank);
  assert.ok(positions.get(step(scene,'exit').id).rank>positions.get(step(scene,'launch').id).rank);
  assert.ok(positions.get(step(scene,'decode').id).rank<positions.get(step(scene,'check').id).rank);
});
test('missing CFG is explicit, never substituted with an unordered call neighborhood',()=>{
  const r=fixture();r.controlFlows=[];
  const scene=endpointWalkthrough(r,endpoint);
  assert.equal(scene.nodes.length,2);
  assert.match(scene.nodes[1].label,/unavailable/i);
  assert.equal(scene.nodes.some(n=>n.calls?.length),false);
});
test('a panic-only callee terminates the expanded caller path instead of fabricating a continuation',()=>{
  const r=fixture(),callee=r.controlFlows.find(f=>f.symbolID==='service');
  callee.nodes=callee.nodes.filter(n=>n.id!=='ret');
  callee.edges=[{from:'entry',to:'panic',kind:'sequence'},{from:'panic',to:'exit',kind:'panic'}];
  const base=endpointWalkthrough(r,endpoint),site=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::second']});
  assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='call'));
  assert.equal(scene.edges.some(e=>e.from===site.id&&e.to===step(base,'again').id),false);
  assert.equal(scene.nodes.some(n=>n.ownerID==='service'&&n.step?.kind==='exit'),false);
  assert.equal(scene.edges.some(e=>e.kind==='return-to-caller'),false);
});
test('two expansions of a repeated callee have distinct bodies and return to their own continuation',()=>{
  const r=fixture(),base=endpointWalkthrough(r,endpoint),first=step(base,'check'),second=step(base,'again');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[first.id+'::second',second.id+'::repeat']});
  assert.equal(scene.nodes.filter(n=>n.ownerID==='service'&&n.step?.kind==='entry').length,2);
  for(const [site,next] of [[first,second],[second,step(scene,'defer')]]) {
    const entry=scene.edges.find(e=>e.from===site.id&&e.kind==='call').to;
    const instance=scene.nodes.find(n=>n.id===entry).instance;
    assert.ok(scene.edges.some(e=>e.kind==='return-to-caller'&&e.to===next.id&&
      scene.nodes.find(n=>n.id===e.from).instance===instance));
  }
});
test('short-circuit metadata takes precedence over a truncated source label',()=>{
  const r=fixture(),source=r.controlFlows[0].nodes.find(n=>n.id==='check');
  source.callOrderKnown=false;source.label='longExpressionWithTruncatedCondition…';
  const base=endpointWalkthrough(r,endpoint),site=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::second']});
  assert.equal(step(scene,'check').orderBoundary,true);
  assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='inspection'));
});
test('old snapshots without invocation classification cannot claim synchronous call and return',()=>{
  const r=fixture();delete r.calls.find(c=>c.id==='second').invocation;
  const base=endpointWalkthrough(r,endpoint),site=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::second']});
  assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='inspection'));
  assert.equal(scene.edges.some(e=>e.kind==='return-to-caller'),false);
});
test('expanded nonreturning calls remove caller steps that no longer have a possible path',()=>{
  const r=fixture(),callee=r.controlFlows.find(f=>f.symbolID==='helper');
  callee.nodes=callee.nodes.filter(n=>n.id!=='ret');
  callee.edges=[{from:'entry',to:'panic',kind:'sequence'},{from:'panic',to:'exit',kind:'panic'}];
  const base=endpointWalkthrough(r,endpoint),site=step(base,'decode');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::first']});
  assert.equal(scene.nodes.some(n=>n.step?.id==='check'),false);
  assert.equal(scene.nodes.some(n=>n.ownerID==='handler'&&n.step?.kind==='exit'),false);
});
test('recursive bodies unfold only on explicit occurrences and stop with an inspectable boundary',()=>{
  const r=fixture(),callee=r.controlFlows.find(f=>f.symbolID==='service');
  callee.nodes.splice(1,0,{id:'recur',kind:'statement',label:'Check()',callIDs:['recursive'],reachable:true});
  callee.edges[0].to='recur';callee.edges.push({from:'recur',to:'ret',kind:'sequence'});
  r.calls.push({id:'recursive',from:'service',to:'service',kind:'static',invocation:'synchronous'});
  let scene=endpointWalkthrough(r,endpoint),expanded=[];
  expanded.push(step(scene,'check').id+'::second');
  for(let depth=0;depth<11;depth++) {
    scene=endpointWalkthrough(r,endpoint,{expanded});
    const sites=scene.nodes.filter(n=>n.calls?.[0]?.id==='recursive'&&!expanded.includes(n.id+'::recursive'));
    if(!sites.length)break;
    expanded.push(sites[0].id+'::recursive');
  }
  assert.ok(scene.nodes.some(n=>n.kind==='boundary'&&/limit/.test(n.label)));
  assert.ok(scene.nodes.length<70);
  assert.equal(new Set(scene.nodes.map(n=>n.id)).size,scene.nodes.length);
  assert.equal(walkthroughPositions(scene).size,scene.nodes.length);
});
test('compact mode retains every call occurrence and branch label',()=>{
  const r=fixture(),full=endpointWalkthrough(r,endpoint,{compact:false}),compact=endpointWalkthrough(r,endpoint);
  const calls=scene=>scene.nodes.flatMap(n=>n.calls.map(c=>c.id)).sort();
  assert.deepEqual(calls(compact),calls(full));
  assert.deepEqual(compact.edges.filter(e=>e.kind==='branch').map(e=>e.label).sort(),['false','true']);
});
test('legacy overlapping call identities create an explicit boundary rather than duplicate callee instances',()=>{
  const r=fixture(),source=r.controlFlows[0].nodes.find(n=>n.id==='check');
  source.callIDs=['second','second'];source.callOrderKnown=true;
  r.calls.find(c=>c.id==='second').evaluationOrder=1;
  const base=endpointWalkthrough(r,endpoint),site=step(base,'check');
  const scene=endpointWalkthrough(r,endpoint,{expanded:[site.id+'::second']});
  assert.equal(new Set(scene.nodes.map(n=>n.id)).size,scene.nodes.length);
  assert.equal(scene.nodes.filter(n=>n.ownerID==='service'&&n.step?.kind==='entry').length,1);
  assert.ok(scene.limitations.some(text=>/call-site identities.*reanalyze/i.test(text)));
  assert.ok(scene.edges.some(e=>e.from===site.id&&e.kind==='inspection'));
});
