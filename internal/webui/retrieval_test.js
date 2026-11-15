const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

const tick=()=>new Promise(resolve=>setImmediate(resolve));
function setup(t,fetch) {
  const dom=new JSDOM(fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),{runScripts:'outside-only'});
  t.after(()=>dom.window.close());
  dom.window.fetch=fetch;dom.window.AbortController=AbortController;
  const ctx=dom.getInternalVMContext();
  vm.runInContext(`const $=id=>document.getElementById(id);
    let report={module:'sample',files:[{id:'file',path:'sample.go',source:'package sample\\nfunc F() {}'}],symbols:[{id:'f',name:'F',kind:'function'}],documents:[],testSources:[]},mode={kind:'logical',id:'f'};
    function render(){} function go(next){mode=next;render();}
    function el(tag,text,parent){const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(parent)parent.append(e);return e;}
    function button(text,fn,parent){const e=el('button',text,parent);e.type='button';e.onclick=fn;return e;}
    function detail(title,text){$('detail').replaceChildren();el('h2',title,$('detail'));el('p',text,$('detail'));}
    function openDocumentation(doc){globalThis.openedDocument=doc.id;}`,ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname,'retrieval.js'),'utf8'),ctx);
  return {dom,ctx,doc:dom.window.document};
}

test('disabled retrieval preserves literal search and never starts inference',async t=>{
  const calls=[];
  const {doc}=setup(t,async url=>{calls.push(url);return {ok:true,json:async()=>({enabled:false,state:'disabled'})};});
  await tick();
  assert.equal(doc.querySelector('#retrieval-prepare').disabled,true);
  assert.match(doc.querySelector('#retrieval-status').textContent,/embeddings.json/);
  assert.ok(calls.every(url=>url.startsWith('/api/retrieval/status')),'browsing started indexing/query inference');
  assert.equal(doc.querySelector('#search').type,'search');
});

test('explicit preparation shows truthful progress and results navigate retained identities',async t=>{
  let state='not-ready',starts=0;
  const {ctx,doc}=setup(t,async(url,options)=>{
    if(url.startsWith('/api/retrieval/status'))return {ok:true,json:async()=>({enabled:true,state,snapshot:{id:'snap'},status:{state,completed:1,total:4,profile:{dimensions:3}}})};
    if(url==='/api/retrieval/index'){starts++;state='indexing';return {ok:true,json:async()=>({state:'preparing'})};}
    if(url==='/api/retrieval/query'){
      assert.equal(JSON.parse(options.body).snapshotID,'snap');
      return {ok:true,json:async()=>({snapshotID:'snap',hits:[{chunk:{kind:'symbol',entityID:'f',fileID:'file',name:'F',startLine:2,endLine:2},reasons:['lexical']}],nextOffset:null,limitations:['Capability unverified']})};
    }
    throw Error('Unexpected endpoint '+url);
  });
  await tick();
  assert.equal(starts,0);
  doc.querySelector('#retrieval-prepare').click();await tick();await tick();
  assert.equal(starts,1);
  assert.equal(doc.querySelector('#retrieval-progress').max,4);
  assert.equal(doc.querySelector('#retrieval-progress').value,1);
  assert.equal(doc.querySelector('#retrieval-cancel').hidden,false);
  state='ready';await vm.runInContext('retrievalLoadStatus()',ctx);
  doc.querySelector('#retrieval-query').value='some behavior';
  await vm.runInContext('retrievalSearch()',ctx);
  assert.match(doc.querySelector('#retrieval-results').textContent,/Capability unverified/);
  doc.querySelector('#retrieval-results button').click();
  assert.equal(vm.runInContext('mode.id',ctx),'f');
});

test('stale semantic replies cannot replace a changed query',async t=>{
  let resolveQuery;
  const {ctx,doc}=setup(t,async url=>{
    if(url.startsWith('/api/retrieval/status'))return {ok:true,json:async()=>({enabled:true,state:'ready',snapshot:{id:'snap'}})};
    return await new Promise(resolve=>{resolveQuery=resolve;});
  });
  await tick();
  doc.querySelector('#retrieval-query').value='old query';
  const pending=vm.runInContext('retrievalSearch()',ctx);
  await tick();
  doc.querySelector('#retrieval-query').value='new query';
  doc.querySelector('#retrieval-query').dispatchEvent(new doc.defaultView.Event('input'));
  resolveQuery({ok:true,json:async()=>({snapshotID:'snap',hits:[{chunk:{entityID:'old',name:'OLD_STALE_RESULT'},reasons:[]}],limitations:[]})});
  await pending;
  assert.ok(!doc.querySelector('#retrieval-results').textContent.includes('OLD_STALE_RESULT'));
});
