const test=require('node:test');
const assert=require('node:assert/strict');
const changes=require('./change-tools.js');

const comparison={id:'comparison',revisions:{before:{commit:'before'},after:{commit:'after'}},scope:{},limitations:['Partial static analysis'],
  modules:[{id:'m',path:'.',before:{snapshotID:'b'},after:{snapshotID:'a'}}],
  files:[{id:'file',status:'changed',content:'text',before:{path:'app.go'},after:{path:'app.go'},
    items:[{id:'i',fileID:'file',kind:'text',before:{start:3,lines:1},after:{start:3,lines:1}}]}],
  records:[{id:'r',title:'Hidden helper',kind:'implementation',moduleID:'m',itemIDs:['i'],fileIDs:['file'],flowIDs:['f','g']}],
  flows:[{id:'f',name:'First',moduleID:'m',status:'matched',associations:[{recordID:'r',side:'after',quality:'potential-impact',witness:[{kind:'source-call',from:'entry',to:'helper'}]}]},
    {id:'g',name:'Second',moduleID:'m',status:'matched',associations:[]}],correspondences:[]};
const state={comparisonID:'comparison',version:2,items:{i:'reviewed'},flows:{f:true}};

test('changed stages and hidden implementation annotations use source witnesses, not geometry',()=>{
  const c=structuredClone(comparison);
  c.records[0].after={symbolID:'helper'};c.records[0].before={symbolID:'helper-before'};
  const nodes=[{id:'one',symbolID:'entry'},{id:'two',symbolID:'helper'},{id:'three',symbolID:'unrelated'}];
  const annotations=changes.annotate(c,'f','after',nodes);
  assert.equal(annotations[0].change.label,'Changes within');
  assert.equal(annotations[1].change.label,'Changed');
  assert.equal(annotations[2].change,undefined);
  assert.equal(annotations[0].change.recordIDs[0],'r');
});

test('comparison discovery keeps unique progress separate from flow consideration',()=>{
  const result=changes.query(comparison,state,'overview',{});
  assert.equal(result.progress.total,1);
  assert.equal(result.progress.reviewed,1);
  assert.equal(result.progress.considered,1);
  assert.equal(result.progress.flows,2);
  assert.equal(changes.query(comparison,state,'records',{perspective:'shared'}).total,1);
  assert.equal(changes.query(comparison,state,'records',{status:'remaining'}).total,0);
  assert.equal(changes.query(comparison,state,'records',{query:'nonexistent'}).filteredOut,1);
  assert.equal(state.version,2,'reads cannot change local progress');
});

test('comparison queries validate identities and expose retained witnesses and paged text',()=>{
  assert.equal(changes.query(comparison,state,'witnesses',{flowID:'f',recordID:'r'}).items[0].witness[0].to,'helper');
  assert.equal(changes.query(comparison,state,'record',{recordID:'r'}).itemCount,1);
  for(const args of [{comparisonID:'old'},{side:'current'},{moduleID:'foreign'},{recordID:'foreign'}])
    assert.throws(()=>changes.query(comparison,state,'overview',args));
  assert.throws(()=>changes.query(comparison,state,'records',{status:'approved'}));
  const text='λ'.repeat(7000);
  const page=changes.text(text,{textOffset:6000,textLimit:100});
  assert.equal(page.text.length,100);
  assert.equal(page.textPage.nextOffset,6100);
});

test('long inclusion paths remain retrievable without breaking discovery byte limits',()=>{
  const c=structuredClone(comparison);
  c.flows[0].associations[0].witness=Array.from({length:400},(_,i)=>({kind:'source-call',from:'s'+i,to:'s'+(i+1),location:{fileID:'source',line:i+1}}));
  const overview=changes.query(c,state,'witnesses',{flowID:'f'});
  assert.equal(overview.items[0].witnessCount,400);
  assert.ok(Buffer.byteLength(JSON.stringify(overview))<6144);
  const steps=[];let offset=0;
  while(offset!==null){
    const page=changes.query(c,state,'witnesses',{flowID:'f',section:'steps',associationIndex:0,offset});
    steps.push(...page.items);offset=page.nextOffset;
  }
  assert.equal(steps.length,400);assert.equal(steps.at(-1).to,'s400');
});
