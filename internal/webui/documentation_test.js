const test=require('node:test');
const assert=require('node:assert/strict');
const docs=require('./documentation-tools.js');
const report={
  root:'/repo/server',documentationRoot:'/repo',module:'example.test/builder',
  files:[{id:'f',path:'internal/app/scriptcheck/check.go',package:'example.test/builder/internal/app/scriptcheck'}],
  symbols:[{id:'check',name:'Check',kind:'method',qualified:'func (*example.test/builder/internal/app/scriptcheck.Checker).Check()',
    signature:'func (c *Checker) Check()',package:'example.test/builder/internal/app/scriptcheck',location:{fileID:'f'}}],
  documents:[
    {id:'nested',path:'server/README.md',text:'# Backend'},
    {id:'arch',path:'docs/architecture.md',text:'Uses `server/internal/app/scriptcheck`.'},
    {id:'route',path:'docs/script-nodes.md',text:'`POST /api/script-nodes/check` calls `scriptcheck`.'},
    {id:'source',path:'docs/design.md',text:'See [implementation](../server/internal/app/scriptcheck/check.go).'},
    {id:'symbol',path:'docs/check.md',text:'`Checker.Check` returns diagnostics.'},
    {id:'local',path:'server/internal/app/scriptcheck/README.md',text:'# Local package'},
    {id:'other',path:'docs/other.md',text:'An unrelated Checker checks things.'},
    {id:'root',path:'README.md',text:'# Builder'}
  ]
};
test('overview prefers root README and retains explicit document selection',()=>{
  assert.equal(docs.overviewDocument(report).id,'root');
  assert.equal(docs.overviewDocument(report,'arch').id,'arch');
  assert.equal(docs.overviewDocument({...report,documents:[]}),null);
  assert.equal(docs.overviewDocument({...report,documents:[report.documents[1]]}).id,'arch');
});
test('contextual docs expose explicit source, package, symbol and colocated associations',()=>{
  const rows=docs.relatedDocuments(report,{symbolID:'check'});
  assert.deepEqual(rows.map(r=>r.document.id).sort(),['arch','local','route','source','symbol']);
  assert.ok(rows.find(r=>r.document.id==='source').reasons.some(r=>r.includes('Source path')));
  assert.ok(rows.find(r=>r.document.id==='symbol').reasons.some(r=>r.includes('Checker.Check')));
  assert.ok(rows.find(r=>r.document.id==='local').reasons.some(r=>r.includes('Package documentation')));
  assert.equal(rows.some(r=>r.document.id==='other'),false);
});
test('endpoint and package contexts find docs without requiring symbol selection',()=>{
  assert.ok(docs.relatedDocuments(report,{endpoint:{patternKnown:true,pattern:'POST /api/script-nodes/check'}})
    .some(r=>r.document.id==='route'&&r.reasons.some(r=>r.includes('Route'))));
  assert.ok(docs.relatedDocuments(report,{package:'example.test/builder/internal/app/scriptcheck'})
    .some(r=>r.document.id==='arch'));
  assert.ok(docs.relatedDocuments(report,{package:'example.test/builder/internal/app/scriptcheck'})
    .some(r=>r.document.id==='local'));
  assert.deepEqual(docs.relatedDocuments(report,{endpoint:{patternKnown:false,patternExpression:'dynamic()'}}),[]);
});
test('associations respect token boundaries and do not invent short-name symbol matches',()=>{
  const other={...report,documents:[{id:'no',path:'docs/other.md',
    text:'`scriptchecker` and `OtherChecker.Check` in server/internal/app/scriptcheck-other.'}]};
  assert.deepEqual(docs.relatedDocuments(other,{symbolID:'check'}),[]);
});
test('reader context changes with code selection but not viewport or endpoint filtering',()=>{
  const mode={kind:'endpoint',id:'route',selected:'step-a',query:'check'};
  const key=docs.contextKey(mode);
  assert.equal(docs.contextKey({...mode,viewport:{x:50,y:100},query:'other'}),key);
  assert.notEqual(docs.contextKey({...mode,selected:'step-b'}),key);
  assert.notEqual(docs.contextKey({...mode,id:'other-route'}),key);
  assert.notEqual(docs.contextKey({kind:'dataflow',id:'function',value:'a'}),
    docs.contextKey({kind:'dataflow',id:'function',value:'b'}));
});
test('package-qualified function mentions associate without matching bare function names',()=>{
  const r={...report,symbols:[{...report.symbols[0],id:'new',kind:'function',name:'New',
    qualified:'func example.test/builder/internal/app/scriptcheck.New()'}],
    documents:[{id:'yes',path:'docs/construction.md',text:'Use `scriptcheck.New`.'},
      {id:'no',path:'docs/unrelated.md',text:'New creates an object.'}]};
  assert.deepEqual(docs.relatedDocuments(r,{symbolID:'new'}).map(r=>r.document.id),['yes']);
});
