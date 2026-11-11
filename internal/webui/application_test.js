const test=require('node:test');
const assert=require('node:assert/strict');
const {endpointInventory}=require('./application-tools.js');

function fixture() {
  const symbols=[
    {id:'handler',name:'handle',kind:'function',package:'m/http'},
    {id:'decode',name:'decode',kind:'function',package:'m/http'},
    {id:'service',name:'Check',kind:'method',package:'m/app'},
    {id:'validate',name:'validate',kind:'function',package:'m/app'}
  ];
  return {symbols,references:[],calls:[
    {id:'a',from:'handler',to:'decode',kind:'static',location:{offset:1}},
    {id:'b',from:'handler',to:'service',kind:'static',location:{offset:2}},
    {id:'c',from:'service',to:'validate',kind:'static',location:{offset:3}},
    {id:'d',from:'service',to:'service',kind:'static',location:{offset:4}},
    {id:'e',from:'handler',kind:'indirect',target:'dynamic',location:{}}
  ],httpEndpoints:[
    {id:'get',pattern:'GET /api/items/{id}',patternKnown:true,handlerID:'handler',handler:'api.handle'},
    {id:'post',pattern:'POST /api/items',patternKnown:true,handlerID:'handler'},
    {id:'all',pattern:'/health',patternKnown:true},
    {id:'host',pattern:'GET example.com/status',patternKnown:true},
    {id:'unknown',patternExpression:'prefix + path',patternKnown:false,reason:'dynamic'},
    {id:'duplicate',pattern:'GET /api/items/{id}',patternKnown:true}
  ]};
}
test('endpoint inventory groups actual paths, retains duplicate registrations and unknowns',()=>{
  const rows=endpointInventory(fixture());
  assert.equal(rows.length,6);
  assert.equal(rows.find(r=>r.id==='get').group,'/api/items');
  assert.equal(rows.find(r=>r.id==='get').method,'GET');
  assert.equal(rows.find(r=>r.id==='all').method,'ANY');
  assert.equal(rows.find(r=>r.id==='host').path,'/status');
  assert.equal(rows.find(r=>r.id==='host').host,'example.com');
  assert.equal(rows.find(r=>r.id==='unknown').group,'Dynamic patterns');
  assert.equal(rows.filter(r=>r.pattern==='GET /api/items/{id}').length,2);
  assert.equal(endpointInventory(fixture(),{query:'POST',group:'/api/items'}).length,1);
  assert.equal(endpointInventory({}).length,0);
});
