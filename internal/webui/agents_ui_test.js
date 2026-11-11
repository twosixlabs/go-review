const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

function loadChat(ctx) {
  for(const name of ['vendor/marked.umd.js','vendor/purify.min.js','markdown.js','chat-tools.js','chat.js'])
    vm.runInContext(fs.readFileSync(path.join(__dirname,name),'utf8'),ctx,{filename:name});
}

test('agent actions reject stale snapshots, arbitrary views and invalid retained identifiers',()=>{
  const tools=require('./chat-tools.js');
  const report={module:'sample',documents:[{id:'doc',text:'Purpose'}],symbols:[],packages:[]};
  assert.throws(()=>tools.validate(report,{view:'document',id:'doc',snapshotID:'old'},'new'),/snapshot/);
  assert.throws(()=>tools.validate(report,{view:'shell',snapshotID:'new'},'new'),/view/);
  assert.throws(()=>tools.validate(report,{view:'document',id:'missing',snapshotID:'new'},'new'),/document/i);
  assert.equal(tools.validate(report,{view:'document',id:'doc',snapshotID:'new'},'new').view,'document');
});

for(const automatic of [false,true])test('chat permission '+automatic+' controls navigation and retains safe browser session history',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const ctx=dom.getInternalVMContext(),doc=dom.window.document;
  dom.window.TextDecoder=TextDecoder;dom.window.AbortController=AbortController;
  let submitted;
  dom.window.fetch=async(url,options)=>{
    if(url==='/api/chat/status')return {ok:true,json:async()=>({enabled:true,model:'ornith',snapshot:{id:'snap',module:'sample'}})};
    submitted=JSON.parse(options.body);
    const data=[
      {type:'tool',tool:'review_document',args:{id:'doc'}},
      {type:'turn_start'},
      {type:'text',text:'# Discarded draft'},
      {type:'discard_turn'},
      {type:'text',text:'# Purpose\n\n**Retained'},
      {type:'text',text:' evidence** and `code` [document:doc]\n\n- One\n- Two\n\n| Scope | CC |\n|---|---|\n| Module | 43 |\n\n```go\nfunc main() {}\n```\n\n<img src=x onerror=alert(1)> [unsafe](javascript:alert(1)) [local](/api/unit-tests) [external](https://example.com)'},
      {type:'navigation',navigation:{snapshotID:'snap',view:'document',id:'doc',label:'Purpose',automatic}},
      {type:'done'}].map(e=>JSON.stringify(e)+'\n').join('');
    let done=false;
    return {ok:true,body:{getReader:()=>({releaseLock:()=>{},read:async()=>done?{done:true}:(done=true,{value:new TextEncoder().encode(data),done:false})})}};
  };
  vm.runInContext(`const $=id=>document.getElementById(id);
    let report={module:'sample',documents:[{id:'doc',text:'Purpose'}],symbols:[],packages:[]},mode={kind:'application'},history=[];
    function render(){} function go(next){history.push(mode);mode=next;render();}
    function openDocumentation(d){go({...mode,documentID:d.id,documentOpen:true});}
    function sym(id){return report.symbols.find(s=>s.id===id);}
    function el(tag,text,parent,cls){const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;if(parent)parent.append(n);return n;}
    function button(text,fn,parent){const n=el('button',text,parent);n.type='button';n.onclick=fn;return n;}`,ctx);
  try{
    loadChat(ctx);
    doc.querySelector('#workspace-chat').click();
    await new Promise(resolve=>setImmediate(resolve));
    doc.querySelector('#chat-input').value='Explain **purpose**';
    doc.querySelector('#chat-allow-navigation').checked=automatic;
    doc.querySelector('#chat-form').dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
    await new Promise(resolve=>setImmediate(resolve));await new Promise(resolve=>setImmediate(resolve));
    assert.equal(submitted.allowNavigation,automatic);
    assert.equal(submitted.snapshotID,'snap');
    if(!automatic)assert.equal(vm.runInContext('mode.documentOpen',ctx),undefined,'proposal alone changed the view');
    assert.equal(doc.querySelectorAll('#chat-messages img').length,0,'model text became executable HTML');
    const body=doc.querySelector('.chat-assistant .chat-text');
    assert.equal(body.querySelector('h1')?.textContent,'Purpose','streamed Markdown heading not rendered');
    assert.equal(body.querySelector('strong')?.textContent,'Retained evidence');
    assert.equal(body.querySelectorAll('li').length,2);
    assert.equal(body.querySelector('td')?.textContent,'Module');
    assert.equal(body.querySelector('pre code')?.textContent,'func main() {}\n');
    assert.equal(body.querySelector('a[href^="javascript:"]'),null);
    assert.equal(body.querySelector('a[href^="/"]'),null,'generated links must not access application routes');
    assert.equal(body.querySelector('a[href="https://example.com"]')?.rel,'noopener noreferrer');
    assert.ok(!body.textContent.includes('Discarded draft'));
    assert.equal(doc.querySelector('.chat-user .chat-text').textContent,'Explain **purpose**','user input must remain literal');
    if(!automatic){doc.querySelector('.chat-navigation').click();await new Promise(resolve=>setImmediate(resolve));}
    assert.equal(vm.runInContext('mode.documentID',ctx),'doc');
    assert.ok(dom.window.sessionStorage.getItem('go-review-chat:snap'));
    assert.ok(doc.querySelector('.chat-citation'),'evidence citation is not actionable');
    assert.ok(doc.querySelector('.chat-tool .chat-citation'),'retrieved evidence needs a link even when the model omits citation syntax');
    assert.equal(doc.querySelector('#chat-stop').disabled,true);
    await vm.runInContext('chatState.snapshot=null;chatLoad()',ctx);
    assert.equal(doc.querySelector('.chat-assistant .chat-text h1')?.textContent,'Purpose','saved history lost Markdown rendering');
    assert.ok(doc.querySelector('.chat-assistant .chat-citation'),'restored evidence citation lost');
    doc.querySelector('#chat-clear').click();
    assert.equal(vm.runInContext('chatState.lastNavigation',ctx),undefined,'Clear chat retained a navigation acknowledgement');
  }finally{dom.window.close();}
});

test('Stop preserves the original cancellation diagnostic without an unhandled cleanup rejection',async()=>{
  const dom=new JSDOM(fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const ctx=dom.getInternalVMContext(),doc=dom.window.document;
  dom.window.TextDecoder=TextDecoder;dom.window.AbortController=AbortController;
  dom.window.fetch=async(url,options)=>{
    if(url.endsWith('/status'))return {ok:true,json:async()=>({enabled:true,model:'ornith',snapshot:{id:'s'}})};
    const error=new DOMException('Request aborted','AbortError');
    return {ok:true,body:{getReader:()=>({releaseLock:()=>{},cancel:async()=>{throw error;},
      read:()=>new Promise((resolve,reject)=>options.signal.addEventListener('abort',()=>reject(error),{once:true}))})}};
  };
  vm.runInContext(`const $=id=>document.getElementById(id);let report={module:'x'},mode={kind:'application'},history=[];
    function render(){} function el(tag,text,parent,cls){const n=document.createElement(tag);if(text)n.textContent=text;if(cls)n.className=cls;if(parent)parent.append(n);return n;}
    function button(text,fn,parent){const n=el('button',text,parent);n.onclick=fn;return n;}`,ctx);
  try{
    loadChat(ctx);
    doc.querySelector('#workspace-chat').click();await new Promise(r=>setImmediate(r));
    doc.querySelector('#chat-input').value='Explore';
    doc.querySelector('#chat-form').dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
    await new Promise(r=>setImmediate(r));
    doc.querySelector('#chat-stop').click();await new Promise(r=>setImmediate(r));
    assert.match(doc.querySelector('#chat-status').textContent,/Stopped/);
    assert.equal(doc.querySelector('#chat-stop').disabled,true);
  }finally{dom.window.close();}
});

for(const interruption of ['manual navigation','Stop'])test(interruption+' during navigation validation prevents a late automatic action',async()=>{
  const dom=new JSDOM(fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const ctx=dom.getInternalVMContext(),doc=dom.window.document;
  dom.window.TextDecoder=TextDecoder;dom.window.AbortController=AbortController;
  let statusCalls=0;
  dom.window.fetch=async url=>{
    if(url.endsWith('/status')) {
      if(++statusCalls===3) {
        if(interruption==='Stop')doc.querySelector('#chat-stop').click();
        else vm.runInContext("mode={kind:'packages'}",ctx);
      }
      return {ok:true,json:async()=>({enabled:true,model:'local',snapshot:{id:'s'}})};
    }
    let done=false;
    const data=new TextEncoder().encode(JSON.stringify({type:'navigation',
      navigation:{snapshotID:'s',view:'document',id:'doc',automatic:true}})+'\n'+JSON.stringify({type:'done'})+'\n');
    return {ok:true,body:{getReader:()=>({releaseLock:()=>{},
      read:async()=>done?{done:true}:(done=true,{value:data,done:false})})}};
  };
  vm.runInContext(`const $=id=>document.getElementById(id);
    let report={module:'sample',documents:[{id:'doc'}]},mode={kind:'application'},history=[];
    function render(){} function openDocumentation(d){mode={kind:'application',documentID:d.id};}
    function el(tag,text,parent,cls){const n=document.createElement(tag);if(text)n.textContent=text;if(cls)n.className=cls;if(parent)parent.append(n);return n;}
    function button(text,fn,parent){const n=el('button',text,parent);n.onclick=fn;return n;}`,ctx);
  try {
    loadChat(ctx);
    doc.querySelector('#workspace-chat').click();await new Promise(r=>setImmediate(r));
    doc.querySelector('#chat-input').value='Show purpose';
    doc.querySelector('#chat-allow-navigation').checked=true;
    await doc.querySelector('#chat-form').onsubmit({preventDefault(){}});
    assert.equal(vm.runInContext('mode.documentID',ctx),undefined,'late action replaced the user-controlled view');
    assert.match(doc.querySelector('#chat-status').textContent,interruption==='Stop'?/Stopped/:/View changed manually/);
  }finally{dom.window.close();}
});
