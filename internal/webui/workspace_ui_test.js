const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

async function workspace(options={}) {
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  if(options.tutorialSeen)dom.window.localStorage.setItem('go-review-tutorial:v1','seen');
  const loc={fileID:'f',line:3,column:1,endLine:3,endColumn:26};
  const snapshot={root:'/repo',module:'example/app',sourceSHA256:'sha',documentationRoot:'/repo',
    packages:[{path:'example/app',name:'app',fileIDs:['f'],imports:[],doc:'Application package.'}],
    symbols:[{id:'run',name:'Run',kind:'function',package:'example/app',location:loc,signature:'func Run()',cyclomatic:1}],
    files:[{id:'f',path:'app.go',package:'example/app',source:'package app\n\nfunc Run() { return }\n'}],
    calls:[],systemFlow:{components:[],operations:[],exchanges:[],limitations:[]},
    documents:[{id:'readme',path:'README.md',text:'# Project\nDocumentation body.'}],
    httpEndpoints:[],warnings:[],limitations:[],commands:[],operationSources:{callables:[],calls:[]},
    controlFlows:[{symbolID:'run',entry:'start',nodes:[
      {id:'start',kind:'entry',label:'Run',reachable:true,location:loc,callIDs:[]},
      {id:'exit',kind:'exit',label:'Return',reachable:true,location:loc,callIDs:[]}],
    edges:[{from:'start',to:'exit',kind:'sequence'}]}]};
  let executions=0;
  dom.window.fetch=async(url,options)=>{
    if(options?.method==='POST')executions++;
    if(url==='/api/review')return {ok:true,json:async()=>({running:false,run:null})};
    if(url==='/api/chat/status')return {ok:true,json:async()=>({enabled:false,snapshot:{id:'snap'}})};
    if(url==='/api/dataflow')return {ok:true,json:async()=>({sourceSHA256:'sha',symbolID:'run',nodes:[],edges:[]})};
    return {ok:true,json:async()=>snapshot};
  };
  for(const match of html.matchAll(/<link[^>]+href="\/([^"]+\.css)"/g)) {
    const style=dom.window.document.createElement('style');
    style.textContent=fs.readFileSync(path.join(__dirname,match[1]),'utf8');
    dom.window.document.head.append(style);
  }
  for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
    vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
  await new Promise(resolve=>setImmediate(resolve));
  return {dom,doc:dom.window.document,run:code=>vm.runInContext(code,dom.getInternalVMContext()),executions:()=>executions};
}

test('structural workspace assigns explorer diagram and inspector their own columns',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    run("go({kind:'packages'})");
    const style=dom.window.getComputedStyle(doc.querySelector('main'));
    assert.equal(style.gridTemplateAreas,'"explorer canvas inspector"','the inspector must not wrap below a two-column grid');
    assert.equal(dom.window.getComputedStyle(doc.querySelector('#canvas')).gridArea,'canvas');
    assert.equal(dom.window.getComputedStyle(doc.querySelector('#list')).gridArea,'explorer');
    assert.equal(dom.window.getComputedStyle(doc.querySelector('#detail')).gridArea,'inspector');
    doc.querySelector('#workspace-evidence').click();
    assert.equal(doc.body.classList.contains('review-inspector-open'),false);
    run('render()');
    assert.equal(doc.body.classList.contains('review-inspector-open'),false,'a render must respect explicitly collapsed details');
  }finally{dom.window.close();}
});

test('navigation groups expose review destinations without opening the advanced tools wall',async()=>{
  const {dom,doc}=await workspace();
  try {
    const nav=doc.querySelector('[aria-label="Review sections"]');
    assert.ok(nav,'primary review destinations need discoverable navigation');
    for(const id of ['application','system-context','system-components','packages','review-map','architecture-signals','hygiene','docs','review-results'])
      assert.ok(nav.contains(doc.getElementById(id)),id+' is still buried among advanced settings');
    assert.equal(doc.querySelector('[aria-label="Explore structure"] button').id,'application');
    assert.ok(doc.querySelector('[aria-label="Assess code"]').contains(doc.querySelector('#review-map')));
    assert.equal(doc.querySelector('#application').getAttribute('aria-current'),'page');
    assert.equal(doc.querySelector('#back').disabled,true,'Back at the root must not look actionable');
    doc.querySelector('#packages').click();
    assert.equal(doc.querySelector('#application').hasAttribute('aria-current'),false);
    doc.querySelector('#application').click();
    assert.equal(doc.querySelector('#application').getAttribute('aria-current'),'page');
    assert.equal(doc.querySelector('#advanced-tools').open,false);
  }finally{dom.window.close();}
});

test('function view actions use the selected callable and breadcrumbs restore the package context',async()=>{
  const {dom,doc,run,executions}=await workspace();
  try {
    run("go({kind:'packages'})");
    assert.equal(doc.querySelector('#function-flow').disabled,true,'no function selected; control flow must not silently do nothing');
    assert.match(doc.querySelector('#function-view-hint').textContent,/select a function/i);
    run("go({kind:'package',id:'example/app'})");
    const functionItem=doc.querySelector('#list [data-callable-id="run"]');
    assert.ok(functionItem,'selecting a package must expose its functions, not repeat the package catalog');
    doc.querySelector('#list').scrollTop=72;
    functionItem.click();
    assert.equal(doc.querySelector('#function-flow').disabled,false);
    doc.querySelector('#function-flow').click();
    assert.equal(run('mode.kind'),'symbol','control flow must open from a logical function');
    assert.equal(run('mode.id'),'run');
    assert.ok(doc.querySelector('#graph .flow-node'),'control-flow detail was not rendered');
    const packageCrumb=[...doc.querySelectorAll('#breadcrumb button')].find(b=>b.textContent==='example/app');
    assert.ok(packageCrumb,'package ancestor must be clickable');
    packageCrumb.click();
    assert.equal(run('mode.kind'),'package');
    assert.equal(doc.querySelector('#list').scrollTop,72,'return must preserve explorer position');
    assert.equal(executions(),0,'navigation must not run target code');
  }finally{dom.window.close();}
});

test('documentation and review diagnostics use readable content space without losing code context',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    run("go({kind:'logical',id:'run'})");
    doc.querySelector('#docs').click();
    assert.ok(doc.body.classList.contains('workspace-reader'),'documentation needs more than a narrow sidebar');
    assert.match(doc.querySelector('#detail').textContent,/Documentation body/);
    const close=[...doc.querySelectorAll('#detail button')].find(b=>b.textContent==='Return to code');
    assert.ok(close);close.click();
    assert.equal(run('mode.kind'),'logical');
    assert.equal(run('mode.id'),'run');
    assert.equal(doc.body.classList.contains('workspace-reader'),false);
    doc.querySelector('#function-flow').click();
    doc.querySelector('#data-flow').click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(run('mode.kind'),'dataflow');
    doc.querySelector('#call-map').click();
    assert.equal(run('mode.kind'),'symbol');
    assert.equal(run('preferControl'),false);
  }finally{dom.window.close();}
});

test('Application documentation uses the same wide reader as other views',async()=>{
  const {dom,doc,run}=await workspace({tutorialSeen:true});
  try {
    run("go({kind:'application'})");
    for(const action of ['navigation','overview']) {
      if(action==='navigation')doc.querySelector('#docs').click();
      else [...doc.querySelectorAll('#application-overview button')].find(b=>b.textContent==='Project purpose & full documentation').click();
      assert.equal(run('mode.kind'),'application');
      assert.ok(doc.body.classList.contains('workspace-reader'));
      assert.equal(dom.window.getComputedStyle(doc.querySelector('main')).gridTemplateColumns,
        'minmax(280px,.8fr) minmax(480px,1.2fr)','Application must not override the reader with a 360px inspector');
      assert.match(doc.querySelector('#detail').textContent,/Documentation body/);
      [...doc.querySelectorAll('#detail button')].find(b=>b.textContent==='Close details').click();
      assert.equal(doc.body.classList.contains('workspace-reader'),false);
    }
  }finally{dom.window.close();}
});

test('package maps start at readable node scale and keep an explicit full-map fit action',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    Object.defineProperty(doc.querySelector('#graph'),'clientWidth',{value:1200});
    Object.defineProperty(doc.querySelector('#graph'),'clientHeight',{value:700});
    run(`report.packages=Array.from({length:36},(_,i)=>({path:'example/app/p'+i,name:'p'+i,fileIDs:[],imports:[]}));
      go({kind:'packages'});`);
    const scale=1200/run('pan.w');
    assert.ok(320*scale>=240,'initial fit makes package labels illegible');
    assert.ok(run('pan.w < activeScene.bounds.w'),'initial view must not shrink a large map to fit every package');
    assert.equal(doc.querySelector('#fit').textContent,'Fit all');
    doc.querySelector('#fit').click();
    assert.equal(run('pan.w'),run('activeScene.bounds.w'),'reviewer can still request the complete overview');
  }finally{dom.window.close();}
});

test('review map stays available without a function selection and after filtering',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    for(const kind of ['application','packages','signals','review']) {
      run(`go({kind:'${kind}'})`);
      assert.equal(doc.querySelector('#review-map').disabled,false,kind+' must allow the repository review map');
      for(const id of ['logical-flow','function-flow','data-flow','call-map'])
        assert.equal(doc.getElementById(id).disabled,true,kind+' has no selected callable');
    }
    doc.querySelector('#review-name').value='missing';
    doc.querySelector('#review-name').dispatchEvent(new dom.window.Event('input'));
    assert.equal(doc.querySelector('#review-map').disabled,false);
    assert.equal(doc.querySelector('#review-map').getAttribute('aria-pressed'),'true');
    run("go({kind:'packages'})");
    doc.querySelector('#review-map').click();
    assert.equal(run('mode.kind'),'review');
  }finally{dom.window.close();}
});

test('function view highlights match the visible view across every switching direction',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    const ids=['logical-flow','function-flow','data-flow','call-map','review-map'];
    const pressed=()=>ids.filter(id=>doc.getElementById(id).getAttribute('aria-pressed')==='true');
    for(const origin of ids)for(const destination of ids) {
      run("go({kind:'logical',id:'run'})");
      doc.getElementById(origin).click();
      await new Promise(resolve=>setImmediate(resolve));
      doc.getElementById(destination).click();
      await new Promise(resolve=>setImmediate(resolve));
      assert.deepEqual(pressed(),[destination],origin+' → '+destination+' has stale active buttons');
      for(const id of ids)assert.equal(doc.getElementById(id).disabled,false,id);
    }
    run("go({kind:'packages'})");
    assert.deepEqual(pressed(),[]);
  }finally{dom.window.close();}
});

test('function controls require callable source and control flow requires an available graph',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    run("report.symbols.push({...report.symbols[0],id:'type',kind:'type',name:'Config'});go({kind:'symbol',id:'type'})");
    for(const id of ['logical-flow','function-flow','data-flow','call-map']) {
      assert.equal(doc.getElementById(id).disabled,true,id+' must not inspect a type as a function');
      assert.equal(doc.getElementById(id).getAttribute('aria-pressed'),'false',id+' must not claim an active function view');
    }
    run("report.controlFlows=[];go({kind:'logical',id:'run'})");
    assert.equal(doc.querySelector('#function-flow').disabled,true,'control flow needs a function body graph');
    assert.match(doc.querySelector('#function-flow').title,/no .*control.flow/i);
    assert.equal(doc.querySelector('#call-map').disabled,false);
    doc.querySelector('#call-map').click();
    assert.equal(doc.querySelector('#call-map').getAttribute('aria-pressed'),'true');
  }finally{dom.window.close();}
});

test('evidence collection does not enable function controls without a selected function',async()=>{
  const {dom,doc,run}=await workspace();
  try {
    run("go({kind:'packages'})");
    await run("collect('/api/coverage',{path:'/fixture/coverage.out'},'Loading coverage')");
    for(const id of ['logical-flow','function-flow','data-flow','call-map'])
      assert.equal(doc.getElementById(id).disabled,true,id+' must still require a selected function');
    assert.equal(doc.querySelector('#review-map').disabled,false);
  }finally{dom.window.close();}
});

test('first-use tutorial can be cancelled and remembers dismissal without changing review state',async()=>{
  const {dom,doc,run,executions}=await workspace();
  try {
    const tour=doc.querySelector('#tutorial-dialog');
    assert.ok(tour,'first use must offer an introductory walkthrough');
    assert.equal(tour.hidden,false);
    const before=run('JSON.stringify(mode)');
    doc.querySelector('#tutorial-next').click();
    assert.match(doc.querySelector('#tutorial-progress').textContent,/2 of/);
    doc.dispatchEvent(new dom.window.KeyboardEvent('keydown',{key:'Tab',bubbles:true}));
    assert.equal(doc.activeElement.id,'tutorial-skip','Tab must remain inside the tutorial');
    doc.querySelector('#tutorial-back').click();
    assert.match(doc.querySelector('#tutorial-progress').textContent,/1 of/);
    doc.querySelector('#tutorial-skip').click();
    assert.equal(tour.hidden,true);
    assert.equal(dom.window.localStorage.getItem('go-review-tutorial:v1'),'seen');
    run('render()');
    assert.equal(tour.hidden,true,'renders must not restart a dismissed tour');
    assert.equal(run('JSON.stringify(mode)'),before);
    assert.equal(executions(),0);
  }finally{dom.window.close();}
});

test('Tutorial replays for the current view and completion is remembered',async()=>{
  const {dom,doc,run,executions}=await workspace({tutorialSeen:true});
  try {
    assert.equal(doc.querySelector('#tutorial-dialog').hidden,true);
    for(const kind of ['packages','logical','symbol','review','signals','system-flow','dataflow']) {
      run(`go({kind:'${kind}',id:'run'})`);
      await new Promise(resolve=>setImmediate(resolve));
      const before=run('JSON.stringify(mode)');
      const requests=executions();
      doc.querySelector('#workspace-tutorial').click();
      assert.equal(doc.querySelector('#tutorial-dialog').hidden,false);
      assert.ok(doc.querySelector('#tutorial-title').textContent);
      doc.querySelector('#tutorial-next').click();
      const titles={packages:'Explore packages and functions',logical:'Follow local implementations',
        symbol:'Inspect this function',review:'Compare measured evidence',signals:'Inspect findings, not just scores',
        'system-flow':'Explore structure and exchanges',dataflow:'Trace definitions and uses'};
      assert.equal(doc.querySelector('#tutorial-title').textContent,titles[kind]);
      doc.dispatchEvent(new dom.window.KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
      assert.equal(doc.querySelector('#tutorial-dialog').hidden,true);
      assert.equal(run('JSON.stringify(mode)'),before);
      assert.equal(executions(),requests,'tutorial must not request analysis or evidence collection');
      assert.equal(doc.activeElement.id,'workspace-tutorial');
    }
    doc.querySelector('#workspace-tutorial').click();
    for(let i=0;i<10&&!doc.querySelector('#tutorial-dialog').hidden;i++)doc.querySelector('#tutorial-next').click();
    assert.equal(doc.querySelector('#tutorial-dialog').hidden,true,'Finish must close the walkthrough');
    assert.equal(dom.window.localStorage.getItem('go-review-tutorial:v1'),'seen');
    await new Promise(resolve=>setImmediate(resolve));
  }finally{dom.window.close();}
});

test('tutorial teaches different evidence-reading and drill-down workflows for each view',async()=>{
  const {dom,doc,run}=await workspace({tutorialSeen:true});
  try {
    const cases=[
      ["{kind:'application'}",/entry point/i,/startup/i],
      ["{kind:'package',id:'example/app'}",/function/i,/imports|call/i],
      ["{kind:'logical',id:'run'}",/reference/i,/same.package/i],
      ["{kind:'symbol',id:'run'}",/branch/i,/statement/i],
      ["{kind:'dataflow',id:'run'}",/definition/i,/consumer/i],
      ["{kind:'review'}",/horizontal/i,/marker/i],
      ["{kind:'signals'}",/finding/i,/source/i],
      ["{kind:'signals',section:'hygiene'}",/skipped/i,/check/i],
      ["{kind:'system-flow',level:'components'}",/contract/i,/operation/i]
    ];
    for(const [state,meaning,action] of cases) {
      run(`go(${state})`);await new Promise(resolve=>setImmediate(resolve));
      doc.querySelector('#workspace-tutorial').click();
      const lessons=run("tutorialSteps.slice(1).map(s=>s[1]).join('\\n')");
      assert.match(lessons,meaning,state+' must explain its notation');
      assert.match(lessons,action,state+' must explain an investigation action');
      assert.ok(run('tutorialSteps.length')>=7,'view-specific instruction must go beyond the generic overview');
      doc.querySelector('#tutorial-skip').click();
    }
    await new Promise(resolve=>setImmediate(resolve));
  }finally{dom.window.close();}
});
