const test=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('real retained comparison supports hidden/shared changes, side evidence and separate local progress',
  {skip:!process.env.GO_REVIEW_CHANGE_URL},async()=>{
  const url=process.env.GO_REVIEW_CHANGE_URL;
  const html=await(await fetch(url)).text();
  const dom=new JSDOM(html,{runScripts:'outside-only',url});
  const window=dom.window;
  let confirmations=0;
  window.confirm=()=>{confirmations++;return true;};
  window.fetch=(path,options)=>fetch(new URL(path,url),options);
  const until=async(check,label)=>{
    for(let i=0;i<200;i++){if(check())return;await new Promise(resolve=>setTimeout(resolve,10));}
    throw Error(label+' · '+window.document.querySelector('#change-notice').textContent);
  };
  try{
    const ctx=dom.getInternalVMContext();
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(await(await fetch(url+'/'+match[1])).text(),ctx,{filename:match[1]});
    window.document.dispatchEvent(new window.Event('DOMContentLoaded'));
    await until(()=>vm.runInContext('!!report',ctx),'ordinary report load');
    const ordinary=vm.runInContext('JSON.stringify(mode)',ctx);
    await window.changeOpen();
    const initial=await window.changeQuery('overview');
    assert.equal(initial.progress.reviewed,0);assert.equal(initial.progress.considered,0);
    assert.equal(window.document.querySelectorAll('#change-list button').length,2);
    window.document.querySelector('#change-list button').click();
    await until(()=>window.document.querySelector('#change-flow svg'),'pinned side flow');
    const fullContext=[...window.document.querySelectorAll('#change-flow-tools button')].find(b=>b.textContent==='Show full flow context');
    assert.ok(fullContext?.disabled,'full-context action should describe an already complete flow rather than pretending to reveal more');
    assert.match(window.document.querySelector('#change-flow').textContent,/Changes within/,'hidden helper lost its enclosing stage annotation');
    await until(()=>window.document.querySelector('#change-detail').textContent.includes('helper'),'flow does not expose hidden changed responsibilities');
    await window.document.querySelector('#change-flow .change-node').onclick();
    const focus=[...window.document.querySelectorAll('#change-detail button')].find(b=>b.textContent==='Focus this responsibility');
    await focus.onclick();
    const restoreContext=[...window.document.querySelectorAll('#change-flow-tools button')].find(b=>b.textContent==='Show full flow context');
    assert.equal(restoreContext.disabled,false,'focused scope cannot return to its owning full flow');
    await restoreContext.onclick();
    assert.equal([...window.document.querySelectorAll('#change-flow-tools button')].find(b=>b.textContent==='Show full flow context').disabled,true);
    await window.changePerspective('shared');
    window.document.querySelector('#change-list button').click();
    await until(()=>window.document.querySelector('#change-detail h3')?.textContent.includes('helper'),'shared implementation selection');
    const compare=[...window.document.querySelectorAll('#change-detail button')].find(b=>b.textContent==='Compare retained file evidence');
    compare.click();
    await until(()=>window.document.querySelectorAll('#change-reader pre').length===2,'paired reader');
    const source=[...window.document.querySelectorAll('#change-reader pre')].map(n=>n.textContent);
    assert.match(source[0],/return 1/);assert.match(source[1],/return 2/);
    const logical=[...window.document.querySelectorAll('#change-detail button')].find(b=>b.textContent==='before: Logical flow');
    assert.ok(logical,'pinned implementation has no Logical Flow drill-in');
    await logical.onclick();
    assert.match(window.document.querySelector('#change-flow-tools').textContent,/before.*Logical flow/);
    assert.ok(window.document.querySelector('#change-flow svg'),'logical detail did not produce an interactive source projection');
    assert.equal(confirmations,0,'opening evidence requested a progress/collection action');
    const mark=[...window.document.querySelectorAll('#change-detail button')].find(b=>b.textContent.startsWith('Mark reviewed'));
    await mark.onclick();
    await until(()=>window.document.querySelector('#change-progress').textContent.startsWith('1 of'),'unique implementation review');
    const progress=await window.changeQuery('progress');
    assert.equal(progress.reviewed,1);assert.equal(progress.considered,0);
    assert.equal(confirmations,1,'local progress was not explicitly confirmed exactly once');
    window.document.querySelector('#change-return').click();
    assert.equal(vm.runInContext('JSON.stringify(mode)',ctx),ordinary,'ordinary navigation was mutated');
  }finally{window.close();}
});
