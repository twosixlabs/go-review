const test=require('node:test');
const assert=require('node:assert/strict');
const {reviewRecords,reviewPlot}=require('./review-tools.js');

function fixture() {
  const symbol=(id,kind,cyclomatic,line,pkg='mod/a')=>({
    id,name:id,qualified:pkg+'.'+id,kind,cyclomatic,package:pkg,
    location:{fileID:'f',line,column:1,endLine:line,endColumn:20}
  });
  return {
    symbols:[symbol('weighted','function',6,1),symbol('zero','method',120,2),
      symbol('unknown','closure',1,3),symbol('same','test',6,1,'mod/b'),
      symbol('type','type',6,1),symbol('no-body','function',undefined,4)],
    coverage:{blocks:[
      {fileID:'f',startLine:1,startColumn:1,endLine:1,endColumn:8,statements:3,count:4},
      {fileID:'f',startLine:1,startColumn:8,endLine:1,endColumn:20,statements:1,count:0},
      {fileID:'f',startLine:2,startColumn:1,endLine:2,endColumn:20,statements:5,count:0}
    ]}
  };
}
test('review map uses weighted coverage and raw complexity, excluding unmeasured callables explicitly',()=>{
  const report=fixture(),before=JSON.stringify(report),model=reviewRecords(report);
  assert.deepEqual(model.records.map(r=>[r.symbol.id,r.coverage.value,r.complexity]),[
    ['weighted',75,6],['zero',0,120],['unknown',null,1],['same',75,6]
  ]);
  assert.equal(model.unmeasured,1);
  assert.equal(JSON.stringify(report),before);
});
test('review plot separates missing coverage from zero and never caps high complexity',()=>{
  const plot=reviewPlot(reviewRecords(fixture()).records);
  const weighted=plot.groups.find(g=>g.members[0].symbol.id==='weighted');
  const zero=plot.groups.find(g=>g.members[0].symbol.id==='zero');
  const unknown=plot.groups.find(g=>g.members[0].symbol.id==='unknown');
  assert.equal(weighted.y,160);
  assert.equal(zero.y,460);assert.equal(zero.x,1040);
  assert.equal(unknown.y,540);assert.equal(unknown.x,100);
  assert.equal(plot.maxComplexity,120);
  assert.equal(weighted.members.length,2);
  assert.notEqual(zero.key,unknown.key);
});
test('review plot filters exact package and case-insensitive name without changing coordinate scales',()=>{
  const records=reviewRecords(fixture()).records;
  const plot=reviewPlot(records,{package:'mod/b',query:'SAME'});
  assert.deepEqual(plot.records.map(r=>r.symbol.id),['same']);
  assert.equal(plot.maxComplexity,120);
  assert.equal(plot.groups[0].x,100+5/119*940);
  assert.equal(reviewPlot(records,{package:'mod'}).records.length,0);
  assert.equal(reviewPlot(records,{query:'not found'}).groups.length,0);
});
test('empty/no-profile review maps keep missing evidence neutral with finite scales and stable cohorts',()=>{
  const report=fixture();delete report.coverage;
  const records=reviewRecords(report).records,plot=reviewPlot(records);
  assert.ok(plot.groups.every(g=>g.y===540));
  assert.ok(records.every(r=>r.coverage.value===null));
  assert.equal(reviewPlot([]).maxComplexity,5);
  assert.deepEqual(reviewPlot([]).groups,[]);
  assert.equal(plot.groups.find(g=>g.members[0].complexity===6).key,
    reviewPlot(records,{package:'mod/b'}).groups[0].key);
});
