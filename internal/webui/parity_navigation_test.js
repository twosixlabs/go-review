const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

async function workspace() {
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const ctx=dom.getInternalVMContext(),loc={fileID:'f',line:2,column:1,endLine:2,endColumn:20};
  dom.window.localStorage.setItem('go-review-tutorial:v1','seen');
  const report={root:'/repo',module:'sample',sourceSHA256:'sha',packages:[{path:'sample',name:'sample',imports:[],fileIDs:['f']}],
    files:[{id:'f',path:'app.go',package:'sample',source:'package sample\nfunc Work() {}\n'}],
    symbols:[{id:'work',name:'Work',qualified:'sample.Work',kind:'function',package:'sample',location:loc,cyclomatic:4},
      {id:'global',name:'Global',kind:'variable',package:'sample',location:loc},
      {id:'no-cfg',name:'MissingBody',kind:'function',package:'sample',location:loc}],
    controlFlows:[{symbolID:'work',entry:'start',nodes:[{id:'start',kind:'entry',label:'Work',location:loc,callIDs:[]},{id:'end',kind:'exit',label:'Return',location:loc,callIDs:[]}],edges:[{from:'start',to:'end'}]}],
    calls:[],warnings:['Partial resolution'],limitations:['No execution'],documents:[],httpEndpoints:[],commands:[],testSources:[],
    systemFlow:{sourceSHA256:'sha',components:[],operations:[],exchanges:[],alternatives:[],limitations:[]},
    assessment:{version:'architecture-signals/1',sourceSHA256:'sha',packages:[{package:'sample',level:'watch'}],
      rules:[{id:'r',title:'Rule',description:'Heuristic'}],findings:[{id:'finding',symbolID:'work',package:'sample',ruleID:'r',severity:'low',summary:'Check boundary',evidence:'Source evidence',location:loc}],
      checks:[{id:'check',title:'Types',status:'fail',detail:'Broken type',locations:[loc]}]},
    dataFlows:{sourceSHA256:'sha',results:[{symbolID:'work',status:'available',graph:{symbolID:'work',sourceSHA256:'sha',
      nodes:[{id:'return',kind:'return',label:'return',location:loc}],edges:[],limitations:[]}}]}};
  const run={root:'/repo',status:'complete',modules:[{directory:'/repo',module:'sample'}],steps:[{id:'step',status:'not-run',title:'Verification',detail:'No authorization'}]};
  let posts=0;
  dom.window.fetch=async(url,options)=>{
    if(options?.method==='POST')posts++;
    return {ok:true,json:async()=>url==='/api/review'?{running:false,run}:
      url==='/api/chat/status'?{enabled:true,model:'fixture',snapshot:{id:'snap',module:'sample'}}:report};
  };
  for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
    vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
  await new Promise(resolve=>setImmediate(resolve));
  await vm.runInContext('chatLoad()',ctx);
  return {dom,doc:dom.window.document,run:code=>vm.runInContext(code,ctx),posts:()=>posts};
}

test('chat opens Assess and Evidence destinations with selected retained evidence and no execution',async()=>{
  const w=await workspace();
  try {
    for(const [view,id,kind,panel] of [
      ['review-map','work','review'],['signals','finding','signals'],['hygiene','check','signals'],
      ['results','step',null,'review-results'],['scope',null,null,'scope-results'],['limits',null,null,'evidence'],
      ['control','work','symbol'],['calls','work','symbol'],['dataflow','work','dataflow']]) {
      const action={view,id,snapshotID:'snap',...(view==='dataflow'?{valueID:'return'}:{})};
      assert.equal(await w.run(`chatNavigate(${JSON.stringify(action)})`),true,view);
      if(kind)assert.equal(w.run('mode.kind'),kind,view);
      if(panel)assert.equal(w.run('mode.workspacePanel'),panel,view);
      if(view==='signals')assert.match(w.doc.querySelector('#detail').textContent,/Check boundary/);
      if(view==='hygiene')assert.ok(w.doc.querySelector('[data-check="check"]').open);
      if(view==='results')assert.ok(w.doc.querySelector('[data-review-step="step"]').open);
      if(view==='control')assert.equal(w.run('preferControl'),true);
      if(view==='calls')assert.equal(w.run('preferControl'),false);
      if(view==='dataflow')assert.equal(w.run('mode.value'),'return');
      const context=JSON.parse(w.run('JSON.stringify(chatContext())'));
      if(['control','calls','dataflow'].includes(view))assert.equal(context.symbolID,'work');
    }
    assert.equal(w.posts(),0,'read-only navigation executed a collection request');
  }finally{w.dom.window.close();}
});

test('invalid selected findings checks values and review filters cannot change the current view',async()=>{
  const w=await workspace();
  try {
    for(const action of [{view:'signals',id:'missing'},{view:'hygiene',id:'missing'},
      {view:'results',id:'missing'},{view:'dataflow',id:'work',valueID:'missing'},
      {view:'review-map',id:'work',minComplexity:99},{view:'control',id:'missing'},
      {view:'control',id:'no-cfg'},{view:'calls',id:'global'},{view:'dataflow',id:'global'}]) {
      const before=w.run('JSON.stringify(mode)');
      assert.equal(await w.run(`chatNavigate(${JSON.stringify({...action,snapshotID:'snap'})})`),false);
      assert.equal(w.run('JSON.stringify(mode)'),before);
    }
  }finally{w.dom.window.close();}
});

test('Back restores the previous function view after a chat control/call switch',async()=>{
  const w=await workspace();
  try {
    await w.run(`chatNavigate({view:'control',id:'work',snapshotID:'snap'})`);
    await w.run(`chatNavigate({view:'calls',id:'work',snapshotID:'snap'})`);
    await w.run(`chatNavigate({view:'back',snapshotID:'snap'})`);
    assert.equal(w.run('mode.kind'),'symbol');
    assert.equal(w.run('preferControl'),true);
  }finally{w.dom.window.close();}
});
