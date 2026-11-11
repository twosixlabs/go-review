const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('Change Review stays independent of Application and does not mark progress by browsing',async()=>{
  const dom=new JSDOM(fs.readFileSync(path.join(__dirname,'index.html'),'utf8'),{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  for(const name of ['visual-review.css','changes.css']){
    const style=dom.window.document.createElement('style');style.textContent=fs.readFileSync(path.join(__dirname,name),'utf8');dom.window.document.head.append(style);
  }
  const report={root:'/repo',module:'app',sourceSHA256:'ordinary',packages:[],symbols:[],files:[],calls:[],documents:[],httpEndpoints:[],warnings:[],limitations:[],commands:[]};
  const revisions={before:{label:'base',commit:'a'.repeat(40)},after:{label:'head',commit:'b'.repeat(40)}};
  const requests=[];
  let comparisonID='comparison';
  let releaseSource,sourceGate,recordFixture;
  dom.window.fetch=async(url,options)=>{
    requests.push({url,options});
    const args=options?.body?JSON.parse(options.body):{};
    if(args.operation==='source'&&sourceGate)await sourceGate;
    const value=url==='/api/snapshot'?report:url==='/api/review'?{running:false,run:null}:url==='/api/chat/status'?{enabled:false}:
      url==='/api/changes'?{running:false,comparisonID,revisions}:
      args.operation==='record'&&recordFixture?{id:'many',title:'Shared helper',itemCount:1,fileCount:11,flowCount:11,
        items:{items:['item'],nextOffset:null},
        files:{items:Array.from({length:11},(_,i)=>'file-'+i).slice(args.offset||0,(args.offset||0)+10),nextOffset:args.offset?null:10},
        flows:{items:Array.from({length:11},(_,i)=>'flow-'+i).slice(args.offset||0,(args.offset||0)+10),nextOffset:args.offset?null:10}}:
      args.operation==='side'?{evidence:{path:'README.md',text:args.textOffset?'last page':'first page',textPage:{nextOffset:args.textOffset?null:10}}}:
      args.operation==='overview'?{comparisonID,revisions,fileCount:1,progress:{total:1,reviewed:0,flows:0,considered:0,version:0},
        modules:{items:[],total:0,nextOffset:null},limitations:{items:['No semantic mappings'],nextOffset:null}}:
      {items:[],total:0,nextOffset:null,filteredOut:1};
    return {ok:true,json:async()=>value};
  };
  try{
    const ctx=dom.getInternalVMContext();
    for(const match of fs.readFileSync(path.join(__dirname,'index.html'),'utf8').matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const before=vm.runInContext('JSON.stringify({report,mode})',ctx);
    const destination=dom.window.document.querySelector('#change-review');
    assert.ok(destination,'Change Review must be a persistent Explore destination');
    destination.click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(dom.window.document.querySelector('#change-workspace').hidden,false);
    assert.equal(dom.window.getComputedStyle(dom.window.document.body).overflow,'auto','comparison content is trapped in Application fixed-height shell');
    assert.equal(destination.getAttribute('aria-current'),'page','Change Review is not identified as the active Explore destination');
    assert.notEqual(dom.window.document.querySelector('#application').getAttribute('aria-pressed'),'true','Application remains selected while Change Review is active');
    assert.match(dom.window.document.querySelector('#change-heading').textContent,/base.*head/);
    assert.match(dom.window.document.querySelector('#change-progress').textContent,/0 of 1/);
    assert.equal(vm.runInContext('changeState.perspective',ctx),'files','unmapped repositories should open their inventory rather than an empty flow canvas');
    assert.match(dom.window.document.querySelector('#change-provenance').textContent,/No semantic mappings/,'overview concealed evidence limitations');
    assert.equal(vm.runInContext('chatContext().comparisonID',ctx),'comparison','chat lost the active pinned comparison');
    assert.match(vm.runInContext('tutorialContext()[0]',ctx),/Change Review/,'tutorial is teaching the wrong workspace');
    assert.equal(requests.filter(r=>r.url==='/api/changes/state'||r.url==='/api/changes/collect').length,0);
    assert.equal(vm.runInContext('JSON.stringify({report,mode})',ctx),before,'opening changes replaced ordinary evidence/navigation');
    dom.window.document.querySelector('#change-return').click();
    assert.equal(dom.window.document.querySelector('#change-workspace').hidden,true);
    assert.equal(vm.runInContext('JSON.stringify({report,mode})',ctx),before);
    vm.runInContext('changeState.id=null',ctx);
    await vm.runInContext("changeNavigate({comparisonID:'comparison',operation:'overview'},chatFingerprint())",ctx);
    assert.equal(dom.window.document.querySelector('#change-workspace').hidden,false,'authorized navigation from Application canceled its own transition');
    vm.runInContext("changeState.history=[{record:{id:'old'}}];changeState.flow={id:'old-flow'};changeState.record={id:'old-record'};changeState.fileID='old-file';changeState.detailInput={symbolID:'old'};changeState.expanded=['old-node'];",ctx);
    comparisonID='replacement';
    await vm.runInContext('changeOpen()',ctx);
    assert.equal(vm.runInContext('JSON.stringify({history:changeState.history,flow:changeState.flow,record:changeState.record,fileID:changeState.fileID,detailInput:changeState.detailInput,expanded:changeState.expanded})',ctx),
      JSON.stringify({history:[],flow:null,record:null,fileID:null,detailInput:null,expanded:[]}),
      'replacement inherited stale navigation identities');
    const fingerprint=vm.runInContext('chatFingerprint()',ctx);
    vm.runInContext("changeState.status='remaining'",ctx);
    await assert.rejects(vm.runInContext(`changeNavigate({comparisonID:'replacement',operation:'overview'},${JSON.stringify(fingerprint)})`,ctx),/changed manually/);
    await vm.runInContext("changeShowSide({moduleID:'m',side:'before',snapshotID:'s',view:'document',documentID:'readme'},'Retained document')",ctx);
    const continueText=[...dom.window.document.querySelectorAll('#change-reader button')].find(b=>/Continue/.test(b.textContent));
    assert.ok(continueText,'side reader silently discarded the document continuation');
    await continueText.onclick();
    assert.match(dom.window.document.querySelector('#change-reader').textContent,/first page.*last page/);
    sourceGate=new Promise(resolve=>{releaseSource=resolve;});
    const pendingSource=vm.runInContext("changeCompare('old-file')",ctx);
    await new Promise(resolve=>setImmediate(resolve));
    vm.runInContext("changeAdoptComparison('newer-comparison')",ctx);
    releaseSource();await pendingSource;sourceGate=null;
    assert.equal(dom.window.document.querySelector('#change-reader').childElementCount,0,'late source response repopulated a replaced comparison reader');
    await vm.runInContext("changeNavigate({comparisonID:'newer-comparison',operation:'files',query:'notes',status:'remaining'})",ctx);
    assert.equal(vm.runInContext('changeState.perspective',ctx),'files','agent cannot navigate the complete inventory');
    assert.equal(dom.window.document.querySelector('#change-search').value,'notes','agent search is not reflected in the visible controls');
    assert.equal(dom.window.document.querySelector('#change-filter').value,'remaining');
    assert.doesNotMatch(dom.window.document.querySelector('#change-list').textContent,/No Git changes between/,'an empty filtered page falsely claims the revision inventory is empty');
    recordFixture=true;
    await vm.runInContext("changeSelectRecord('many')",ctx);
    const moreFlows=[...dom.window.document.querySelectorAll('#change-detail button')].find(b=>/More associated flows/.test(b.textContent));
    const moreFiles=[...dom.window.document.querySelectorAll('#change-detail button')].find(b=>/More retained files/.test(b.textContent));
    assert.ok(moreFlows,'later shared-helper flow witnesses are unreachable');
    assert.ok(moreFiles,'later files in a grouped responsibility are unreachable');
    await moreFlows.onclick();await moreFiles.onclick();
    assert.equal([...dom.window.document.querySelectorAll('#change-detail button')].filter(b=>/Show inclusion witness/.test(b.textContent)).length,11);
    assert.equal([...dom.window.document.querySelectorAll('#change-detail button')].filter(b=>/Compare retained file evidence/.test(b.textContent)).length,11);
    const beforeFile=vm.runInContext('chatFingerprint()',ctx);
    await vm.runInContext("changeSelectFile({id:'inventory-file',before:{path:'notes.md'},after:{path:'notes.md'},itemCount:1})",ctx);
    assert.equal(vm.runInContext('chatContext().fileID',ctx),'inventory-file','manual inventory selection is invisible to agent cancellation');
    await assert.rejects(vm.runInContext(`changeNavigate({comparisonID:'newer-comparison',operation:'overview'},${JSON.stringify(beforeFile)})`,ctx),/changed manually/);
    await vm.runInContext("changeCompare('inventory-file')",ctx);
    dom.window.document.querySelector('#change-back').click();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(dom.window.document.querySelector('#change-detail h3').textContent,'notes.md','Back did not restore the inventory selection');
    assert.equal(dom.window.document.querySelector('#change-reader').hidden,true);
    vm.runInContext("changeState.viewport=null;changeDraw({entry:'entry',nodes:[{id:'entry',title:'Entry',kind:'function',metrics:{complexity:{value:null}}}],edges:[]})",ctx);
    const graph=dom.window.document.querySelector('#change-flow svg');
    assert.doesNotMatch(graph.textContent,/CC null/,'unknown complexity is presented as a numeric measurement');
    const original=graph.getAttribute('viewBox').split(' ').map(Number);
    graph.dispatchEvent(new dom.window.WheelEvent('wheel',{deltaX:20,deltaY:40,bubbles:true,cancelable:true}));
    const panned=graph.getAttribute('viewBox').split(' ').map(Number);
    assert.equal(panned[2],original[2],'ordinary wheel unexpectedly zoomed instead of panning');
    assert.notEqual(panned[1],original[1]);
    graph.dispatchEvent(new dom.window.WheelEvent('wheel',{deltaY:-40,ctrlKey:true,bubbles:true,cancelable:true}));
    assert.ok(Number(graph.getAttribute('viewBox').split(' ')[2])<panned[2],'pinch/control-wheel failed to zoom');
    const node=graph.querySelector('g.change-node'),position=node.getAttribute('transform');
    node.dispatchEvent(new dom.window.KeyboardEvent('keydown',{key:'ArrowRight',altKey:true,bubbles:true,cancelable:true}));
    assert.notEqual(node.getAttribute('transform'),position,'keyboard node movement is unavailable');
  }finally{dom.window.close();}
});
