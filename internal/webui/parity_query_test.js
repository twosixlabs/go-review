const test=require('node:test');
const assert=require('node:assert/strict');
const query=require('./review-query-tools.js');
const map=require('./review-tools.js');
const data=require('./dataflow-tools.js');

function fixture() {
  const graph={symbolID:'f',sourceSHA256:'sha',nodes:['input','add','return'].map(id=>({id,kind:id})),
    edges:[{from:'input',to:'add'},{from:'add',to:'return'}],limitations:['No aliases'],collector:'ssa'};
  return {module:'sample',sourceSHA256:'sha',packages:[{path:'sample'}],
    symbols:[{id:'f',name:'Work',qualified:'sample.Work',kind:'function',package:'sample',cyclomatic:12,
      location:{fileID:'file',line:1,endLine:2}},{id:'g',name:'Other',qualified:'sample.Other',kind:'function',package:'sample',cyclomatic:2,location:{fileID:'file',line:3,endLine:4}}],
    dataFlows:{sourceSHA256:'sha',results:[{symbolID:'f',status:'available',graph},
      {symbolID:'g',status:'unsupported',error:'Generic body'}]},
    assessment:{version:'architecture-signals/1',sourceSHA256:'sha',packages:[],checks:[],findings:[],
      rules:[{id:'rule',title:'Boundary',description:'This is a heuristic.'}]}};
}

test('assessment exposes shared map records and cohorts without treating unknown coverage as zero',()=>{
  const r=fixture();
  const result=query.query(r,'assess',{section:'records',package:'sample',minComplexity:10,coverage:'unknown'});
  assert.deepEqual(result.items.map(x=>x.symbol.id),['f']);
  assert.equal(result.items[0].coverage.value,null);
  assert.equal(query.query(r,'assess',{section:'records',maxCoverage:20}).total,0);
  const cohorts=query.query(r,'assess',{section:'cohorts'});
  assert.deepEqual(cohorts.items.map(c=>c.key),map.reviewPlot(map.reviewRecords(r).records).groups.map(c=>c.key));
  assert.equal(query.query(r,'assess',{ruleID:'rule'}).description,'This is a heuristic.');
  assert.equal(query.query(r,'assess',{symbolID:'g',limit:1}).selected.symbol.id,'g');
  const firstCohort=cohorts.items.find(c=>c.complexity===12).key;
  assert.throws(()=>query.query(r,'assess',{symbolID:'g',cohort:firstCohort}),/cohort/);
  assert.throws(()=>query.query(r,'assess',{ruleID:'missing'}),/rule/);
  for(const args of [{coverage:'zero'},{minComplexity:-1},{maxCoverage:101},{package:'missing'},{section:'bogus'}])
    assert.throws(()=>query.query(r,'assess',args));
});

test('retained dataflow query uses the exact UI value slice and pages topology',()=>{
  const r=fixture(),graph=r.dataFlows.results[0].graph;
  const args={symbolID:'f',valueID:'add',direction:'upstream',depth:1};
  const result=query.query(r,'dataflow',args);
  assert.deepEqual(result.nodes,data.slice(graph,'add','upstream',1).nodes);
  assert.deepEqual(result.edges,data.slice(graph,'add','upstream',1).edges);
  assert.equal(query.query(r,'dataflow',{...args,section:'nodes',limit:1}).nextOffset,1);
  assert.equal(query.query(r,'dataflow',{symbolID:'g'}).status,'unsupported');
  assert.equal(query.query({...r,dataFlows:undefined},'dataflow',{symbolID:'f'}).status,'not-collected');
  for(const args of [{symbolID:'g',valueID:'missing'},{symbolID:'f',valueID:'bad'},{symbolID:'f',direction:'bad'},{symbolID:'f',depth:-1},{symbolID:'missing'}])
    assert.throws(()=>query.query(r,'dataflow',args));
  assert.throws(()=>query.query({...r,dataFlows:{...r.dataFlows,sourceSHA256:'old'}},'dataflow',{symbolID:'f'}),/snapshot/);
});

test('repository evidence preserves failures, skipped stages and full paged diagnostics',()=>{
  const run={root:'/repo',status:'complete',modules:[{module:'sample'},{directory:'/bad',error:'Cannot load module'}],
    steps:[{id:'s',status:'not-run',title:'Verification',detail:'Not authorized',commands:[{executable:'go',stdout:'λ'.repeat(15000),stderr:'Error detail'}]}],
    scope:{root:'/repo',excludePaths:['vendor/**']},exclusions:[]};
  assert.equal(query.query({},'results',{},run).moduleCount,2);
  assert.equal(query.query({},'results',{section:'modules'},run).items[1].error,'Cannot load module');
  assert.equal(query.query({},'results',{section:'steps'},run).items[0].status,'not-run');
  assert.equal(query.query({},'results',{stepID:'s',section:'detail'},run).detail,'Not authorized');
  assert.equal(query.query({},'results',{stepID:'s',commandIndex:0,section:'stdout',textOffset:12000},run).stdout.length,3000);
  assert.equal(query.query({},'results',{},null).status,'not-collected');
  assert.throws(()=>query.query({},'results',{stepID:'bad'},run),/step/);
});
