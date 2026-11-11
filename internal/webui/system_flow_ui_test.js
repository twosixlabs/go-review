const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('architectural home navigates context, components and alternatives with source evidence and Back',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const snapshot={root:'/repo',module:'app',sourceSHA256:'source',packages:[],symbols:[],files:[{id:'f',path:'service.go',source:'package app\nfunc Create() {}'}],calls:[],documents:[],httpEndpoints:[{id:'health',pattern:'GET /health',patternKnown:true}],warnings:[],limitations:[],commands:[],systemFlow:{
    sourceSHA256:'source',components:[
      {id:'service',name:'Service',kind:'component',package:'app/service',members:['create'],sources:[{fileID:'f',line:1,endLine:2}]}
    ],operations:[{id:'create-key',name:'Create',componentID:'service',symbolID:'create',location:{fileID:'f',line:1,endLine:2}}],
    exchanges:[],alternatives:[
      {id:'true',componentID:'service',symbolID:'create',decision:'valid',label:'true',execution:'exercised',reason:'Positive unique counter.',location:{fileID:'f',line:2},members:[],witnesses:[],requires:[]},
      {id:'false',componentID:'service',symbolID:'create',decision:'valid',label:'false',execution:'not-observed',reason:'No hit on this arm.',location:{fileID:'f',line:2},members:[],witnesses:[],requires:[]}
    ],limitations:[]
  }};
  snapshot.unitEvidence={sourceSHA256:'source',runs:[{selection:{package:'app/service',tests:['TestCreate']},matchedTests:['TestCreate'],status:'partial',error:'runtime truncated',runtimeTracePath:'/artifacts/runtime.trace',runtime:{
    status:'error',error:'runtime truncated',observations:[],limitations:[]
  }},{selection:{package:'app/service',tests:['TestConcurrent']},matchedTests:['TestConcurrent'],status:'pass',runtime:{
    status:'available',observations:[{kind:'launch',goroutine:'9',relatedGoroutine:'8',componentIDs:['service'],stack:[{function:'Service.Create',location:{fileID:'f',line:1,endLine:2}}],targetStack:[]}],limitations:[]
  }}]};
  let executions=0;
  dom.window.fetch=async (url,options)=>{
    if(options?.method==='POST')executions++;
    return {ok:true,json:async()=>url==='/api/review'?{running:false,run:null}:snapshot};
  };
  try{
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.ok(doc.querySelector('#application-overview h1'),'Application should render its entry-point inventory');
    assert.match(doc.querySelector('#application-overview h1').textContent,/Entry points/);
    assert.match(doc.querySelector('#application-overview').textContent,/GET.*\/health/);
    const startup=[...doc.querySelectorAll('#list button')].find(b=>b.textContent==='Startup & wiring');
    startup.click();
    assert.match(doc.querySelector('#application-overview h1').textContent,/Supporting initialization/);
    doc.querySelector('#application').click();
    const context=[...doc.querySelectorAll('#list button')].find(b=>b.textContent==='System context');
    assert.ok(context,'architecture must remain accessible from the entry-point home');
    context.click();
    assert.match(doc.querySelector('#breadcrumb').textContent,/system context/i);
    const click=label=>{
      const b=[...doc.querySelectorAll('button')].find(b=>b.textContent===label);
      assert.ok(b,'missing navigation '+label);b.click();
    };
    click('Components & exchanges');
    assert.match(doc.querySelector('#application-overview').textContent,/Choose a component group/);
    const filter=doc.querySelector('#system-filter');filter.value='service';
    filter.dispatchEvent(new dom.window.Event('input',{bubbles:true}));
    doc.querySelector('#application-overview').scrollTop=83;
    click('app/service');
    assert.ok([...doc.querySelectorAll('#list button')].some(b=>b.textContent==='Service'),
      'a component must be selectable without fitting a potentially large diagram');
    const node=doc.querySelector('[data-node-id="service"]');
    assert.ok(node);node.dispatchEvent(new dom.window.MouseEvent('click',{bubbles:true}));
    assert.match(doc.querySelector('#detail').textContent,/valid.*true.*exercised/s);
    assert.match(doc.querySelector('#detail').textContent,/false.*not-observed/s);
    doc.querySelector('#application-overview').scrollTop=0;
    assert.match(doc.querySelector('#detail').textContent,/not.*chronolog|not.*sequence/i);
    assert.match(doc.querySelector('#detail').textContent,/runtime truncated/);
    assert.match(doc.querySelector('#detail').textContent,/Standard runtime observations.*error/s);
    assert.match(doc.querySelector('#detail').textContent,/Observed launch · G8 → G9/);
    vm.runInContext('pan={x:22,y:33,w:650,h:500};view();',dom.getInternalVMContext());
    const viewport=doc.querySelector('#graph').getAttribute('viewBox');
    click('Create');
    assert.match(doc.querySelector('#detail').textContent,/Positive unique counter/);
    click('Inspect source · service.go:1');
    assert.match(doc.querySelector('#detail .source-line').textContent,/package app/);
    assert.equal(doc.querySelector('#detail a').href,'vscode://file//repo/service.go:1');
    doc.querySelector('#back').click();
    assert.match(doc.querySelector('#breadcrumb').textContent,/Service/);
    assert.equal(doc.querySelector('#graph').getAttribute('viewBox'),viewport);
    doc.querySelector('#back').click();
    doc.querySelector('#back').click();
    assert.equal(doc.querySelector('#system-filter').value,'service');
    assert.equal(doc.querySelector('#application-overview').scrollTop,83);
    vm.runInContext('report.unitEvidence.allTests=true;report.unitEvidence.runs[0].selection.tests=[];render();',dom.getInternalVMContext());
    assert.match(doc.querySelector('#detail').textContent,/all in-scope tests/);
    assert.equal(executions,0);
  }finally{dom.window.close();}
});
