const test=require('node:test');
const assert=require('node:assert/strict');
const {logicalRelationships,callableTitle}=require('./logical-tools.js');

test('callable labels include receiver type so reviewers can distinguish same-name methods',()=>{
  assert.equal(callableTitle({name:'Check',qualified:'func (*builder/internal/app/scriptcheck.Checker).Check(ctx context.Context) error'}),'Checker.Check');
  assert.equal(callableTitle({name:'Run',qualified:'func (builder/internal/app.runner).Run()'}),'runner.Run');
  assert.equal(callableTitle({name:'New',qualified:'func builder/internal/app.New()'}),'New');
});

function fixture() {
  const symbols=[
    {id:'root',name:'start',kind:'function',package:'m/adapter'},
    {id:'helper',name:'decode',kind:'function',package:'m/adapter'},
    {id:'service',name:'Run',kind:'method',package:'m/service'},
    {id:'callback',name:'handle',kind:'method',package:'m/adapter'},
    {id:'field',name:'State',kind:'type',package:'m/service'}
  ];
  const loc={fileID:'f',line:10,column:1,endLine:10,endColumn:20,offset:10,endOffset:30};
  const call=(id,to,kind='static',offset=10)=>({id,from:'root',to,kind,target:to||'dynamic',execution:'possible',location:{...loc,offset}});
  return {symbols,calls:[call('helper-1','helper'),call('service-1','service'),call('service-2','service','static',40),
    call('self','root'),call('external',undefined),call('dynamic',undefined,'indirect'),call('conversion',undefined,'conversion'),
    {...call('caller','root'),from:'helper'}],
    references:[
      {from:'root',to:'service',name:'Run',usage:'callee',location:loc},
      {from:'root',to:'callback',name:'handle',usage:'value',location:loc},
      {from:'root',to:'callback',name:'handle',usage:'value',location:{...loc,offset:50}},
      {from:'root',to:'field',name:'State',location:loc}
    ]
  };
}
test('logical view groups verified local targets once and preserves every call site and recursion',()=>{
  const report=fixture(),before=JSON.stringify(report),model=logicalRelationships(report,'root');
  assert.deepEqual(model.crossPackage.map(g=>[g.symbol.id,g.calls.length]),[['service',2]]);
  assert.deepEqual(model.samePackage.map(g=>[g.symbol.id,g.calls.length]),[['helper',1],['root',1]]);
  assert.deepEqual(model.references.map(g=>[g.symbol.id,g.references.length]),[['callback',2]]);
  assert.deepEqual(model.boundaries.map(c=>c.id),['external','dynamic']);
  assert.deepEqual(model.conversions.map(c=>c.id),['conversion']);
  assert.deepEqual(model.callers.map(g=>g.symbol.id),['helper']);
  assert.equal(JSON.stringify(report),before);
});
test('function values are not promoted to calls even when the same target is invoked elsewhere',()=>{
  const report=fixture();
  report.references.push({from:'root',to:'service',name:'Run',usage:'value',location:{fileID:'f',offset:70}});
  const model=logicalRelationships(report,'root');
  assert.deepEqual(model.references.map(g=>g.symbol.id),['callback','service']);
  assert.equal(model.crossPackage[0].calls.length,2);
});
test('older references with no syntax usage remain explicit unknowns rather than fabricated callbacks',()=>{
  const report=fixture();delete report.references[1].usage;
  const model=logicalRelationships(report,'root');
  assert.equal(model.unclassifiedReferences.length,1);
  assert.equal(model.references[0].references.length,1);
});
test('same-package-only functions remain navigable and missing targets stay boundaries',()=>{
  const report=fixture();report.calls=report.calls.filter(c=>c.to!=='service');
  report.calls.push({id:'stale',from:'root',to:'missing',kind:'static',target:'Missing',location:{}});
  const model=logicalRelationships(report,'root');
  assert.equal(model.crossPackage.length,0);
  assert.ok(model.samePackage.some(g=>g.symbol.id==='helper'));
  assert.ok(model.boundaries.some(c=>c.id==='stale'));
  assert.equal(logicalRelationships(report,'missing').symbol,undefined);
});
