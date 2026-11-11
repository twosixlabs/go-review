const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

for(const runtimeTrace of [false,true])test('selected unit evidence sends exact names once with optional runtime trace '+runtimeTrace,async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const requests=[];
  const snapshot={root:'/repo',module:'app',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  dom.window.fetch=async (url,options)=>{
    requests.push({url,options});
    if(url==='/api/review')return {ok:true,json:async()=>({running:false,run:null})};
    if(url==='/api/unit-tests')return {ok:true,json:async()=>({...snapshot,unitEvidence:{runs:[{
      selection:{package:'app',tests:['TestGood','TestBad']},matchedTests:['TestGood','TestBad'],status:'partial',error:'selected failure'
    }]}})};
    return {ok:true,json:async()=>snapshot};
  };
  try{
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.equal(doc.querySelector('#observed'),null);
    assert.equal(doc.querySelector('#run-trace'),null);
    if(runtimeTrace)vm.runInContext(
      "report.systemFlow={components:[{id:'service',package:'app/service'}]};mode={kind:'system-flow',componentID:'service'};",
      dom.getInternalVMContext());
    doc.querySelector('#run-tests').click();
    const selectedPackage=runtimeTrace?'app/service':'app';
    assert.equal(doc.querySelector('[aria-label="Unit-test package"]').value,selectedPackage);
    doc.querySelector('[aria-label="Unit-test names"]').value='TestGood, TestBad';
    const runtime=doc.querySelector('[aria-label="Include standard Go runtime trace"]');
    assert.ok(runtime,'runtime evidence cannot be selected in the collection UI');
    assert.equal(runtime.checked,false);
    runtime.checked=runtimeTrace;
    doc.querySelector('[aria-label="Authorize selected unit tests"]').checked=true;
    doc.querySelector('.evidence-form').dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
    await new Promise(resolve=>setImmediate(resolve));
    const executions=requests.filter(r=>r.url==='/api/unit-tests');
    assert.equal(executions.length,1);
    assert.deepEqual(JSON.parse(executions[0].options.body),{execute:true,saveSelections:true,runtimeTrace,selections:[{package:selectedPackage,tests:['TestGood','TestBad']}]});
    vm.runInContext("unitEvidence({id:'production'})",dom.getInternalVMContext());
    const text=doc.querySelector('#detail').textContent;
    assert.match(text,/partial/);
    assert.match(text,/selected failure/);
    assert.doesNotMatch(text,/passing-test observations|baseline|instrumented/i);
  }finally{dom.window.close();}
});
