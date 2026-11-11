const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');
const {endpointFlow,activityOverview}=require('./endpoint-flow-tools.js');
const {hierarchy,project}=require('./visual-review-tools.js');

test('real Builder service or Wasm entries expose major handoffs before function steps',
  {skip:!process.env.GO_REVIEW_ENDPOINT_SNAPSHOT&&!process.env.GO_REVIEW_ENDPOINT_URL},async t=>{
  const liveURL=process.env.GO_REVIEW_ENDPOINT_URL;
  const served=async resource=>{
    const response=await fetch(new URL(resource,liveURL));
    assert.ok(response.ok,resource+': HTTP '+response.status);
    return response.text();
  };
  const report=JSON.parse(liveURL?await served('/api/snapshot'):
    fs.readFileSync(process.env.GO_REVIEW_ENDPOINT_SNAPSHOT,'utf8'));
  const html=liveURL?await served('/'):fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const scripts=await Promise.all([...html.matchAll(/<script src="\/([^"]+)"><\/script>/g)].map(async match=>
    ({name:match[1],source:liveURL?await served('/'+match[1]):fs.readFileSync(path.join(__dirname,match[1]),'utf8')})));
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  dom.window.TextEncoder=TextEncoder;dom.window.TextDecoder=TextDecoder;
  dom.window.fetch=async(url,options)=>{
    assert.notEqual(options?.method,'POST','retained navigation executed target code');
    return {ok:true,json:async()=>url==='/api/review'?{running:false,run:null}:report};
  };
  try {
    for(const script of scripts)
      vm.runInContext(script.source,dom.getInternalVMContext(),{filename:script.name});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    if(report.entryPoints?.kind==='wasm') {
      const {entryInventory}=require('./application-tools.js');
      const entry=entryInventory(report).find(e=>e.entryKind==='wasm');
      assert.ok(entry,'WIT export missing');
      const raw=endpointFlow(report,entry,{reviewActivities:true});
      const handlerCall=report.operationSources.calls.find(c=>c.targetPackage==='builder/internal/cosmonic/handler'&&c.targetName==='Handle');
      assert.ok(handlerCall,'fixture does not contain the business-handler call');
      const boundary=raw.nodes.find(n=>n.callID===handlerCall.callID);
      assert.ok(boundary,'business handler was silently omitted');
      assert.match(boundary.boundary,/not been analyzed/);
      assert.ok(!boundary.symbolID,'external implementation must not acquire local review gauges');
      assert.ok(raw.nodes.some(n=>n.kind==='Prepare information'),'input decoding disappeared');
      const constructors=report.operationSources.calls.filter(c=>c.targetPackage==='go.bytecodealliance.org/pkg/wit/types'&&
        !c.receiver&&['Ok','Err'].includes(c.targetName));
      assert.ok(constructors.length);
      assert.ok(constructors.every(c=>!raw.nodes.some(n=>n.callID===c.callID)),
        'WIT result constructors still dominate the activity overview');
      const entryRow=doc.querySelector('.endpoint-row');
      assert.ok(entryRow);entryRow.click();
      const card=[...doc.querySelectorAll('.review-node')].find(n=>n.querySelector('.activity-select')?.textContent.includes('Handler')&&
        n.querySelector('.activity-select')?.textContent.includes('Handle')&&!n.querySelector('.activity-select')?.textContent.includes('HandleMessage'));
      assert.ok(card,'business handler is absent from rendered flow');card.querySelector('.activity-select').click();
      assert.match(doc.querySelector('#detail').textContent,/not been analyzed/);
      assert.ok(doc.querySelector('#detail').textContent.includes('builder/internal/cosmonic/handler'));
      t.diagnostic('WIT export, input decoding, external business-handler boundary and source-only quality scope verified.');
      return;
    }
    for(const [route,component] of [
      ['POST /api/missions/{missionID}/builds','Builder'],
      ['POST /api/executions','Runner'],
      ['POST /api/gitlab/workflows','WorkflowGit'],
      ['GET /api/missions/{missionID}','Builder']
    ]) {
      const endpoint=report.httpEndpoints.find(e=>e.pattern===route);
      assert.ok(endpoint,route);
      const start=performance.now(),raw=endpointFlow(report,endpoint,{reviewActivities:true}),
        scene=activityOverview(report,raw),elapsed=performance.now()-start;
      assert.ok(elapsed<2000,'entry overview took '+elapsed+' ms');
      assert.ok(scene.nodes.some(n=>n.label.includes(component)),component+' must be visible from the entry point');
      assert.ok(scene.nodes.every(n=>n.operations.every(o=>!scene.nodes.some(peer=>peer.id===o.id))),
        'nested responsibilities must not appear as peer activities');
      assert.deepEqual(new Set(scene.nodes.flatMap(n=>[n.id,...n.operations.map(o=>o.id)])),new Set(raw.nodes.map(n=>n.id)),
        'activity grouping must retain all source operations for review');
      assert.ok(scene.edges.some(e=>e.alternatives.length),'missing branch evidence for '+route);
      assert.ok(scene.nodes.some(n=>n.information.some(i=>i.type.includes('builder/'))),'missing business contracts');
      assert.ok(!scene.nodes.some(n=>['statement','expression','implicit_return'].includes(n.kind)));
      const row=[...doc.querySelectorAll('.endpoint-row')].find(n=>
        n.querySelector('.endpoint-path').textContent===route.slice(route.indexOf(' ')+1)&&
        n.querySelector('.endpoint-method').textContent===route.split(' ')[0]);
      assert.ok(row,route);row.click();
      assert.match(doc.querySelector('#breadcrumb').textContent,/Overall flow/);
      assert.equal(doc.querySelectorAll('.review-node-face [data-metric="coverage"]').length,scene.nodes.length);
      assert.equal(doc.querySelectorAll('.review-node-face [data-metric="architecture"]').length,scene.nodes.length);
      assert.ok(doc.querySelector('.review-connection'),'no visual relationships');
      const stage=scene.nodes.find(n=>n.phase==='operation'&&n.label.includes(component)&&n.symbolID);
      const focusStart=performance.now();
      const focused=endpointFlow(report,endpoint,{reviewActivities:true,symbolID:stage.symbolID});
      const focusElapsed=performance.now()-focusStart;
      assert.ok(focusElapsed<2000,'focused source projection blocked review for '+Math.round(focusElapsed)+' ms');
      assert.ok(focused.nodes.length,'focused responsibility lost its source flow');
      const card=doc.querySelector(`#activity-overview [data-node-id="${stage.id}"]`);
      assert.ok(card,'business responsibility missing');
      card.querySelector('.activity-select').click();
      assert.match(doc.querySelector('#detail').textContent,/Information exchanged/);
      if(stage.operations.length) {
        assert.match(doc.querySelector('#detail').textContent,/Responsibilities & boundaries/);
        const expand=card.querySelector('.review-expand');
        assert.ok(expand,'on-node expansion unavailable');expand.click();
        const expanded=doc.querySelector(`#activity-overview [data-node-id="${stage.id}"]`);
        assert.ok(expanded.querySelector('.review-children .review-node'),'nested source structure missing');
        assert.equal(expanded.querySelector('.review-expand').getAttribute('aria-expanded'),'true');
        expanded.querySelector('.review-expand').click();
        const review=[...doc.querySelectorAll('#detail button')].find(n=>n.textContent==='Review activity');
        review.click();
        assert.ok(doc.querySelectorAll('#activity-overview .review-node').length>1,'activity did not open its own control flow');
        assert.ok(doc.querySelector('.review-parent-flow .review-parent-edge'),'parent relationships lost during focus');
        doc.querySelector('#back').click();
        assert.equal(vm.runInContext('mode.selected',dom.getInternalVMContext()),stage.id);
      }
      if(route==='POST /api/missions/{missionID}/builds') {
        assert.match(card.textContent,/creates or replays a build/i);
        assert.ok(![...doc.querySelectorAll('.activity-select')].some(b=>/Append Event|New String|Fire/.test(b.textContent)),
          'supporting implementation operations still dominate overview');
        assert.ok(doc.querySelector('.review-async-lane .review-node'),'clarification must remain independently visible');
      }
      const drill=[...doc.querySelectorAll('#detail button')].find(n=>n.textContent==='Inspect function steps');
      drill.click();
      assert.match(doc.querySelector('#legend').textContent,/Possible execution flow/);
      doc.querySelector('#back').click();
      assert.match(doc.querySelector('#breadcrumb').textContent,/Overall flow/);
      t.diagnostic(route+': '+scene.nodes.length+' stages; '+scene.edges.length+' possible continuations; '+scene.alternatives.length+' alternatives; '+Math.round(elapsed)+' ms');
      doc.querySelector('#application').click();
    }
  }finally{dom.window.close();}
});

test('collected unrelated service, CLI and library projects use the same visual exploration',
  {skip:!process.env.GO_REVIEW_VISUAL_FIXTURES},async()=>{
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  for(const name of ['service','cli','library']) {
    const snapshot=JSON.parse(fs.readFileSync(path.join(process.env.GO_REVIEW_VISUAL_FIXTURES,name+'.json'),'utf8'));
    const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
    dom.window.fetch=async(url,options)=>{
      assert.notEqual(options?.method,'POST','exploration must not execute target code');
      return {ok:true,json:async()=>url==='/api/review'?{running:false,run:null}:snapshot};
    };
    try {
      for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
        vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
      await new Promise(resolve=>setImmediate(resolve));
      const doc=dom.window.document;
      const entry=doc.querySelector('.endpoint-row');
      assert.ok(entry,name+' entry discovery');entry.click();
      assert.ok(doc.querySelector('.review-connection'),name+' connected flow');
      assert.ok(doc.querySelector('.review-node-face [data-metric="coverage"]'),name+' unknown metrics remain visible');
      const node=doc.querySelector('.review-node[data-symbol-id] .activity-select');node.click();
      assert.ok(doc.querySelector('#detail').textContent.includes('Measurement scope'));
      const endpoint=vm.runInContext('selectedEntryPoint()',dom.getInternalVMContext());
      const tree=hierarchy(snapshot,endpointFlow(snapshot,endpoint,{reviewActivities:true}));
      const projected=project(tree,[]);
      assert.equal(new Set(projected.sourceIDs).size,tree.nodes.length);
      assert.ok(!projected.error);
      if(name==='service') {
        assert.ok(doc.querySelector('.review-async-lane .review-node'),'service goroutine missing');
        const process=[...doc.querySelectorAll('.review-node')].find(n=>n.querySelector('.activity-select').textContent==='Submit');
        assert.ok(process,'service responsibility missing');process.querySelector('.activity-select').click();
        assert.match(doc.querySelector('#detail').textContent,/second paragraph/i);
      }
    }finally{dom.window.close();}
  }
});
