const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('real review renderers expose signals in overviews, graph cards and direct hygiene navigation',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  const location={fileID:'file',line:2,column:1,endLine:2,endColumn:20,offset:10,endOffset:29};
  const report={module:'app',root:'/app',documentationRoot:'/app',sourceSHA256:'source',
    packages:[{path:'app',name:'app',fileIDs:['file'],imports:[],typeErrors:[]}],
    files:[{id:'file',package:'app',path:'app.go',source:'package app\nfunc F() {}\n'}],
    symbols:[{id:'f',package:'app',name:'F',kind:'function',signature:'func F()',cyclomatic:1,location}],
    calls:[],references:[],documents:[],warnings:[],limitations:[],commands:[],inputs:[],
    trace:{outcomesMatch:true,events:[
      {process:'p',goroutine:'g',sequence:1,symbolID:'f',action:'enter'},
      {process:'p',goroutine:'g',sequence:2,symbolID:'f',action:'exit'}
    ],edges:[]},
    httpEndpoints:[{id:'route',patternKnown:true,pattern:'GET /test',handlerID:'f',handler:'F',location}],
    controlFlows:[{symbolID:'f',entry:'entry',nodes:[{id:'entry',kind:'entry',label:'F',location},{id:'exit',kind:'exit',label:'return',location}],
      edges:[{from:'entry',to:'exit'}],limitations:[]}],
    assessment:{version:'architecture-signals/1',sourceSHA256:'source',
      packages:[{package:'app',level:'watch',files:1,functions:1}],
      findings:[{id:'finding',symbolID:'f',package:'app',ruleID:'forwarding',severity:'low',summary:'Review boundary',evidence:'For review',location}],
      rules:[],checks:[{id:'gofmt',title:'Formatting',status:'pass',detail:'Inspected',locations:[]},
        {id:'vet',title:'Go vet',status:'not-run',detail:'Not executed',locations:[]}]}};
  dom.window.fetch=async()=>({ok:true,json:async()=>report});
  try {
    const context=dom.getInternalVMContext();
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),context,{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const document=dom.window.document;
    const run=code=>vm.runInContext(code,context);
    assert.match(document.querySelector('#signal-summary').textContent,/Hygiene/);
    const labels=parent=>[...parent.querySelectorAll('.metric-label')].map(n=>n.textContent);
    assert.deepEqual(labels(document.querySelector('#signal-summary')),['Coverage','Complexity · max','Architecture','Hygiene · 1 not run']);
    assert.equal(document.querySelectorAll('#signal-summary button.metric-gauge').length,0,'signal gauges must use the same inline styling as coverage/complexity');
    assert.equal(document.querySelectorAll('#signal-summary [role="button"].metric-gauge').length,2,'inline signal drill-down must remain accessible');
    assert.equal(document.querySelectorAll('#signal-summary .metric-dial svg').length,4,'overview metrics must share gauge widgets');
    const scopedLabels=parent=>[...parent.querySelectorAll('.review-meter small')].map(n=>n.textContent);
    assert.deepEqual(scopedLabels(document.querySelector('.endpoint-row')),['Coverage','CC · max','Arch']);
    assert.equal(document.querySelectorAll('.endpoint-row [data-metric="hygiene"]').length,0,
      'snapshot-wide hygiene must not masquerade as an endpoint metric');
    assert.equal(document.querySelectorAll('.endpoint-row button').length,0,'overview row must not nest interactive buttons');
    run(`Object.assign(dataEntry(report,'f'),{state:'ready',graph:{symbolID:'f',nodes:[{id:'return',kind:'return',label:'return',location:${JSON.stringify(location)}}],edges:[],limitations:[]}})`);
    for(const mode of [{kind:'packages'},{kind:'package',id:'app'},{kind:'logical',id:'f'},
      {kind:'symbol',id:'f'},{kind:'dataflow',id:'f'},{kind:'observed'},{kind:'endpoint',id:'route',detailLevel:'function'}]) {
      run(`go(${JSON.stringify(mode)})`);
      const nodes=document.querySelectorAll('#graph>.node');
      assert.ok(nodes.length,mode.kind+' should render graph nodes');
      assert.equal(document.querySelectorAll('#graph>.node .node-signals').length,nodes.length,mode.kind+' missing node indicators');
      assert.equal(document.querySelectorAll('#graph>.node .node-signals .signal-dial svg').length,nodes.length*2,mode.kind+' missing architecture/hygiene gauge widgets');
      if(['logical','symbol','dataflow'].includes(mode.kind)) {
        assert.deepEqual(labels(document.querySelector('.context-signal-summary')),['Coverage','Complexity','Architecture','Hygiene · 1 not run'],mode.kind+' detail must keep metrics together in the same order');
        assert.equal(document.querySelectorAll('#detail .metric-gauge').length,4,mode.kind+' detail must not duplicate metrics');
      }
    }
    const previous=run('JSON.stringify(mode)');
    document.querySelector('#graph [data-signal="hygiene"]').dispatchEvent(new dom.window.MouseEvent('click',{bubbles:true}));
    assert.equal(run('mode.section'),'hygiene');
    assert.ok(document.querySelector('#hygiene-results'));
    document.querySelector('#back').click();
    assert.equal(run('JSON.stringify(mode)'),previous,'badge navigation lost endpoint state');
    document.querySelector('#review-map').click();
    assert.equal(document.querySelectorAll('.review-point').length,1);
    assert.equal(document.querySelectorAll('.architecture-marker').length,1);
    run("preferControl=false;go({kind:'symbol',id:'f'})");
    assert.ok(document.querySelector('#graph>.node .signal-dial svg'),'call neighborhood missing gauges');
    document.querySelector('#hygiene').click();
    assert.equal(run('mode.section'),'hygiene');
    assert.equal(document.querySelectorAll('.assessment-summary .metric-dial svg').length,4,'assessment overview missing shared metric row');
    assert.equal(document.querySelectorAll('[data-package] .metric-dial svg').length,4,'package overview row missing shared metrics');
    assert.equal(document.querySelectorAll('[data-package] button').length,0);
    const summary=document.querySelector('[data-package] .overview-metrics');
    assert.deepEqual(labels(summary),['Coverage','Complexity · max','Architecture','Hygiene · 1 not run']);
    run("report.symbols[0].name='init';go({kind:'application',section:'startup'})");
    assert.equal(document.querySelectorAll('.startup-entry .review-meter>svg svg').length,3,'startup overview missing scoped dials');
    run(`report.coverage={blocks:[{fileID:'file',startLine:2,startColumn:1,endLine:2,endColumn:20,statements:4,count:1}]};
      report.symbols.push({...report.symbols[0],id:'second',name:'Other',cyclomatic:20});
      go({kind:'application'})`);
    const captions=parent=>[...parent.querySelectorAll('.metric-caption strong')].map(n=>n.textContent);
    assert.deepEqual(captions(document.querySelector('#signal-summary')).slice(0,2),['100.0%','20']);
    const scopedCaptions=parent=>[...parent.querySelectorAll('.review-meter strong')].map(n=>n.textContent);
    assert.deepEqual(scopedCaptions(document.querySelector('.endpoint-row')).slice(0,2),['100%','1'],'endpoint metrics must stay handler-scoped');
    run("report.httpEndpoints[0].handlerID='missing';render()");
    assert.deepEqual(scopedCaptions(document.querySelector('.endpoint-row')).slice(0,3),['?','?','Not assessed'],'unresolved handler must not inherit repository measurements');
    run("report.review={moduleCount:2};go({kind:'packages'})");
    assert.match(document.querySelector('#signal-summary .signal-scope').textContent,/^Module/,'one module snapshot must not masquerade as an entire multi-module repository');
  } finally {
    dom.window.close();
  }
});
