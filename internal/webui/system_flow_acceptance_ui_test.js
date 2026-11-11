const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

test('real Builder retained view keeps context legible and exposes lifecycle, contracts and alternatives',
  {skip:!process.env.GO_REVIEW_ACCEPTANCE_DIR},async(t)=>{
  const summary=JSON.parse(fs.readFileSync(path.join(process.env.GO_REVIEW_ACCEPTANCE_DIR,'builder-acceptance.json'),'utf8'));
  const run=JSON.parse(fs.readFileSync(summary.review,'utf8'));
  const snapshot=run.modules[0].report;
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  dom.window.fetch=async (url,options)=>{
    assert.notEqual(options?.method,'POST','retained browsing executed code');
    return {ok:true,json:async()=>url==='/api/review'?{running:false,run:null}:snapshot};
  };
  try{
    for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
      vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),dom.getInternalVMContext(),{filename:match[1]});
    await new Promise(resolve=>setImmediate(resolve));
    const doc=dom.window.document;
    assert.match(doc.querySelector('#application-overview h1').textContent,/HTTP endpoints/);
    const context=()=>[...doc.querySelectorAll('#list button')].find(b=>b.textContent==='System context').click();
    context();
    assert.match(doc.querySelector('#breadcrumb').textContent,/system context/i);
    assert.ok(doc.querySelectorAll('#graph .node').length<=6,'system context expanded into implementation detail');
    assert.ok([...doc.querySelectorAll('#graph .edge-label')].every(n=>n.textContent.length<=36),'context labels are too long for the diagram');
    t.diagnostic('Context viewport: '+doc.querySelector('#graph').getAttribute('viewBox'));
    const saveDiagram=name=>{
      doc.querySelector('#fit').click();
      const graph=doc.querySelector('#graph').cloneNode(true);
      graph.setAttribute('xmlns','http://www.w3.org/2000/svg');
      graph.setAttribute('width','1200');graph.setAttribute('height','700');
      const style=doc.createElementNS('http://www.w3.org/2000/svg','style');
      style.textContent=['style.css','exploration.css'].map(file=>fs.readFileSync(path.join(__dirname,file),'utf8')).join('\n')+
        '\nsvg{background:#101722;height:100%;width:100%}';
      graph.prepend(style);
      fs.writeFileSync(path.join(process.env.GO_REVIEW_ACCEPTANCE_DIR,name+'.svg'),graph.outerHTML);
    };
    saveDiagram('builder-context');
    for(const packageName of ['builder/internal/app/assist/build','builder/internal/app/runner','builder/internal/app/workflowgit']) {
      const group=[...doc.querySelectorAll('#list button')].find(b=>b.textContent===packageName);
      assert.ok(group,packageName);group.click();
      const labels=[...doc.querySelectorAll('#list button')].map(b=>b.textContent);
      assert.equal(labels.filter(label=>label==='Builder').length,0,
        'different Builder boundaries require distinguishable component choices');
      const component=snapshot.systemFlow.components.find(c=>c.package===packageName&&c.members.length>0&&['Builder','Runner','WorkflowGit'].includes(c.name));
      const node=doc.querySelector(`[data-node-id="${component.id}"]`);
      assert.ok(node);node.dispatchEvent(new dom.window.MouseEvent('click',{bubbles:true}));
      const operation=packageName.endsWith('/workflowgit')?'Create':'Start';
      const button=[...doc.querySelectorAll('#list button')].find(b=>b.textContent===operation);
      assert.ok(button,operation+' not available on '+component.kind+' '+component.name);button.click();
      assert.match(doc.querySelector('#detail').textContent,/exercised/);
      assert.match(doc.querySelector('#detail').textContent,/not-observed|unknown/);
      assert.ok(doc.querySelectorAll('#graph .edge').length>0,'no architectural information flows');
      assert.ok(doc.querySelectorAll('#graph .node').length<=doc.querySelectorAll('#graph .edge').length+1,
        'unrelated boundaries leaked into the operation neighbourhood');
      t.diagnostic(component.name+'.'+operation+': '+doc.querySelectorAll('#graph .node').length+
        ' nodes; '+doc.querySelectorAll('#graph .edge').length+' exchanges');
      if(packageName.endsWith('/assist/build')) {
        saveDiagram('builder-start');
        const bounds=doc.querySelector('#graph').getAttribute('viewBox').split(' ').map(Number);
        assert.ok(bounds[2]<=1500,'small operation neighbourhood requires excessive horizontal panning');
      }
      doc.querySelector('#application').click();
      context();
    }
    assert.equal(doc.querySelectorAll('#graph .node-gauge').length,0,'implementation gauges overwhelmed the context');
  }finally{dom.window.close();}
});
