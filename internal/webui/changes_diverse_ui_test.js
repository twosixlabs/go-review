const test=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('diverse retained revisions keep every supporting change and unavailable side accessible',
  {skip:!process.env.GO_REVIEW_CHANGE_URL},async()=>{
  const url=process.env.GO_REVIEW_CHANGE_URL,html=await(await fetch(url)).text();
  const dom=new JSDOM(html,{runScripts:'outside-only',url}),window=dom.window;
  window.confirm=()=>true;
  window.fetch=(path,options)=>fetch(new URL(path,url),options);
  const pages=async(operation,args={})=>{
    const items=[];let offset=0;
    while(offset!==null){const value=await window.changeQuery(operation,{...args,offset});items.push(...value.items);offset=value.nextOffset;}
    return items;
  };
  try{
    const ctx=dom.getInternalVMContext();
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(await(await fetch(url+'/'+match[1])).text(),ctx,{filename:match[1]});
    for(let i=0;i<200&&!vm.runInContext('!!report',ctx);i++)await new Promise(resolve=>setTimeout(resolve,10));
    const ordinary=vm.runInContext('JSON.stringify({mode,report})',ctx);
    await window.changeOpen();
    const overview=await window.changeQuery('overview'),files=await pages('files'),records=await pages('records'),flows=await pages('flows');
    assert.ok(files.length>10,'fixture does not exercise discovery continuation');
    assert.equal(files.length,overview.fileCount);
    assert.ok(overview.modules.items.some(m=>m.path==='broken'&&m.after.error),'failed module was concealed');
    const errorFlow=flows.find(f=>f.name.includes('.First('));
    assert.ok(errorFlow,'changed error path has no entry context');
    await window.changeSelectFlow(errorFlow);
    const errorRecord=records.find(r=>r.title.endsWith('.First'));
    assert.ok(errorRecord,'changed error condition was not discoverable');
    await window.changeSelectRecord(errorRecord.id);
    const errorDetail=await window.changeQuery('record',{recordID:errorRecord.id});
    await window.changeCompare(errorDetail.files.items[0],errorDetail);
    const errorSource=[...window.document.querySelectorAll('#change-reader .change-pair pre')].map(e=>e.textContent);
    assert.match(errorSource[0],/if v<0\{return -1\}/);
    assert.match(errorSource[1],/if v<0\{return -2\}/);
    assert.ok(overview.limitations.items.some(l=>/not proof/.test(l)),'source exploration lost runtime counterevidence boundary');
    console.log(JSON.stringify({task:'changed-error-path',workflow:'Change Review',actions:['select affected First flow','select First changed responsibility','compare its retained source'],
      before:'negative helper result returns -1',after:'negative helper result returns -2',
      counterevidence:'static source does not establish that the negative path executes',remaining:overview.progress.total,reviewed:overview.progress.reviewed,
      note:'Scripted evidence-discovery check, not a human usability or speed measurement'}));
    await window.changePerspective('files');
    for(const file of files){
      await window.changeSelectFile(file);
      assert.equal(window.document.querySelector('#change-detail h3').textContent,file.after.path||file.before.path);
      await window.changeCompare(file.id);
      assert.equal(window.document.querySelectorAll('#change-reader .change-pair section').length,2);
      await [...window.document.querySelectorAll('#change-reader button')].find(b=>/targeted Git patch/.test(b.textContent)).onclick();
      assert.match(window.document.querySelector('#change-reader').textContent,/Before:.*After:/s);
    }
    for(const name of ['notes.md','config.json','lib/api.wit','go.mod','app_test.go','inactive.go','generated.go','excluded.go','asset.bin','scripts/verify.sh'])
      assert.ok(files.some(f=>(f.after.path||f.before.path)===name),'missing supporting change '+name);
    assert.equal(files.find(f=>f.after.path==='asset.bin').content,'binary');
    assert.match(files.find(f=>f.after.path==='inactive.go').afterEligibility,/not selected/i);
    assert.match(files.find(f=>f.after.path==='generated.go').afterEligibility,/generated/i);
    assert.match(files.find(f=>f.after.path==='excluded.go').afterEligibility,/excluded/i);
    const removed=records.find(r=>r.title.endsWith('.Gone'));
    assert.ok(removed?.before&&!removed.after,'deleted declaration lost its Before identity');
    await window.changeSelectRecord(removed.id);
    await window.changeCompare((await window.changeQuery('record',{recordID:removed.id})).files.items[0],removed);
    assert.match(window.document.querySelector('#change-reader .change-pair section').textContent,/Gone/);
    const unavailable=flows.find(f=>f.status==='after-unavailable');
    assert.ok(unavailable,'broken module lost its retained Before flow');
    await window.changeSelectFlow(unavailable);
    assert.match(window.document.querySelector('#change-detail').textContent,/after-unavailable/);
    assert.ok([...window.document.querySelectorAll('#change-detail button')].every(b=>!b.textContent.startsWith('Removed:')),'unavailable After analysis was mislabeled as proven removal');
    const repeated=flows.find(f=>f.name.includes('Repeated'));
    assert.ok(repeated,'repeated/async/closure entry disappeared: '+flows.map(f=>f.name).join(', '));
    await window.changeSelectFlow(repeated);
    assert.ok(window.document.querySelector('#change-flow svg'));
    const progress=await window.changeQuery('progress');
    assert.equal(progress.reviewed,0);assert.equal(progress.considered,0);
    const shared=records.find(r=>r.title.endsWith('.helper'));
    assert.ok(shared.flowCount>1,'shared implementation was duplicated or lost');
    await window.changeSelectRecord(shared.id);
    await [...window.document.querySelectorAll('#change-detail button')].find(b=>b.textContent.startsWith('Mark reviewed')).onclick();
    const after=await window.changeQuery('progress');
    assert.ok(after.reviewed>0);assert.equal(after.considered,0);
    await assert.rejects(window.changeRequest('/api/changes/state',{comparisonID:overview.comparisonID,version:0,itemIDs:['foreign'],status:'reviewed',confirmed:true}));
    window.changeClose();
    assert.equal(vm.runInContext('JSON.stringify({mode,report})',ctx),ordinary,'comparison altered Application state');
  }finally{window.close();}
});
