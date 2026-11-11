(function(root) {
  'use strict';
  // This is a source-CFG projection, not a call trace. Helpers are traversed
  // without statement cards; component crossings remain contextual occurrences.
  function endpointFlow(report,endpoint,options={}) {
    const empty={nodes:[],edges:[],alternatives:[],limitations:[]};
    if(!report.systemFlow)return {...empty,error:'Overall flow requires architectural source facts. Analyze this module again.'};
    if(report.systemFlow.sourceSHA256!==report.sourceSHA256)
      return {...empty,error:'Overall flow source identity does not match this snapshot.'};
    const symbols=new Map((report.symbols||[]).map(s=>[s.id,s]));
    const flows=new Map((report.controlFlows||[]).map(f=>[f.symbolID,f]));
    const calls=new Map((report.calls||[]).map(c=>[c.id,c]));
    const typed=new Map((report.operationSources?.calls||[]).map(c=>[c.callID,c]));
    const callables=new Map((report.operationSources?.callables||[]).map(c=>[c.key,c]));
    const callableBySymbol=new Map([...callables.values()].map(c=>[c.symbolID,c]));
    const alternativesBySymbol=new Map();
    for(const a of report.systemFlow.alternatives||[]) {
      if(!alternativesBySymbol.has(a.symbolID))alternativesBySymbol.set(a.symbolID,[]);
      alternativesBySymbol.get(a.symbolID).push(a);
    }
    const components=new Map(),componentByID=new Map();
    for(const c of report.systemFlow.components||[]) {
      componentByID.set(c.id,c);
      for(const id of c.members||[])components.set(id,c);
    }
    const exchanges=new Map();
    for(const e of report.systemFlow.exchanges||[])for(const id of e.callIDs||[]) {
      if(!exchanges.has(id))exchanges.set(id,[]);
      exchanges.get(id).push(e);
    }
    const raw=new Map(),links=[],limitations=new Set([
      'Control arrows are possible source paths, not an observed chronological scenario.',
      'Edge alternatives summarize source paths; several covered alternatives need not belong to one test invocation.',
      'Types and source names describe information contracts, not runtime values or object lineage.',
      'The first component level is opened automatically. Further component operations remain collapsed stages with an overall-flow drill-down.',
      'Deferred execution and cross-goroutine synchronization are not inferred.'
    ]);
    let serial=0;
    const make=(prefix,data={})=>{
      const id=prefix+':'+serial++;
      const n={id,architecture:true,comments:[],alternatives:[],information:[],...data};
      raw.set(id,n);return n;
    };
    const link=(from,to,data={})=>{if(from&&to)links.push({from,to,...data});};
    const ownerName=s=>components.get(s?.id)?.name||s?.package?.split('/').at(-1)||'Unresolved';
    const ownAlternatives=id=>alternativesBySymbol.get(id)||[];
    function contracts(call,target) {
      const t=typed.get(call.id),information=[];
      for(const a of t?.arguments||[])information.push({direction:'input',name:a.value.name||a.value.expression,type:a.value.type});
      const c=callables.get(t?.targetKey)||callableBySymbol.get(target?.id);
      for(const result of c?.results||[])information.push({direction:'output',name:result.value.name,
        type:t?.resultTypes?.[result.position]||result.value.type});
      for(const e of exchanges.get(call.id)||[])for(const i of e.information||[])
        if(!information.some(other=>other.direction===i.direction&&other.type===i.type&&other.name===i.name))information.push(i);
      return information;
    }
    function targetOf(call,callbacks) {
      const callback=callbacks.get(typed.get(call.id)?.targetKey);
      if(callback)return {symbol:symbols.get(callback),resolution:'inferred'};
      if(symbols.has(call.to))return {symbol:symbols.get(call.to),resolution:'resolved'};
      const t=typed.get(call.id),c=callables.get(t?.targetKey);
      if(c&&symbols.has(c.symbolID))return {symbol:symbols.get(c.symbolID),resolution:'resolved'};
      const candidates=new Set();
      for(const e of exchanges.get(call.id)||[]) {
        if(e.resolution!=='inferred')continue;
        for(const id of componentByID.get(e.to)?.members||[])
          if(symbols.get(id)?.name===t?.targetName)candidates.add(id);
      }
      if(candidates.size===1)return {symbol:symbols.get([...candidates][0]),resolution:'inferred'};
      return {resolution:'unresolved'};
    }
    function visibleExternal(call) {
      if(['builtin','conversion'].includes(call.kind))return false;
      const t=typed.get(call.id),pkg=t?.targetPackage||'',text=call.target||'';
      if(endpoint.entryKind==='wasm'&&pkg==='go.bytecodealliance.org/pkg/wit/types'&&
        !t.receiver&&['Ok','Err'].includes(t.targetName)&&
        t.resultTypes?.some(type=>type.startsWith(pkg+'.Result['))) {
        limitations.add('WIT result constructors are collapsed supporting detail; inspect function steps or source for the full return expressions.');
        return false;
      }
      if(call.kind==='indirect'||t?.targetStatus==='function-value')return true;
      if(/(?:encoding\/json\.(?:Decoder|Encoder)\).(?:Decode|Encode)|net\/http\.ResponseWriter\).(?:Write|WriteHeader)|net\/http\.(?:Error|Redirect)|database\/sql|os\.(?:WriteFile|ReadFile|Open|Create))/.test(text))return true;
      if(pkg.startsWith('encoding/json')&&['Decode','Encode'].includes(t?.targetName))return true;
      if(t?.targetOrigin==='standard-library')return false;
      // Older snapshots lack package-origin facts. Keep unknown dependencies
      // visible rather than inferring the standard library from path spelling.
      return pkg!=='';
    }
    function backgroundTarget(callee,callbacks) {
      const candidates=new Set();
      for(const node of flows.get(callee?.id)?.nodes||[])for(const id of node.callIDs||[]) {
        const bound=callbacks.get(typed.get(id)?.targetKey);
        if(bound)candidates.add(bound);
      }
      if(candidates.size!==1)return callee;
      const callback=symbols.get([...candidates][0]);
      const direct=(report.calls||[]).filter(c=>c.from===callback?.id&&symbols.has(c.to));
      return direct.length===1?symbols.get(direct[0].to):callback;
    }
    function callStage(call,target,prefix,phase='operation') {
      const t=typed.get(call.id),targetName=target.symbol?.name||t?.targetName||call.target||'Unresolved call';
      const component=target.symbol?ownerName(target.symbol):
        componentByID.get((exchanges.get(call.id)||[])[0]?.to)?.name||t?.targetPackage||'External boundary';
      const invocation=call.invocation||'unclassified';
      const boundary=!target.symbol?'Implementation is outside the analyzed source; its behavior and quality have not been analyzed.'+
        (t?.reason?' '+t.reason:'')+
        (t?.targetPackage?' Package: '+t.targetPackage+'.':''):undefined;
      if(boundary)limitations.add(boundary);
      return make(prefix,{keep:true,phase,kind:invocation==='goroutine'?'Launch work':
        invocation==='deferred'?'Schedule deferred work':'Component operation',
        label:component+'\n'+targetName,symbolID:target.symbol?.id,componentID:components.get(target.symbol?.id)?.id,
        callID:call.id,location:call.location,invocation,resolution:target.resolution,boundary,
        information:contracts(call,target.symbol),exchanges:exchanges.get(call.id)||[],
        alternatives:target.symbol?[...ownAlternatives(target.symbol.id)]:[]});
    }
    function instantiate(symbolID,prefix,depth,stack,stage,callbacks=new Map(),activityID=stage.id) {
      const owner=symbols.get(symbolID),cfg=flows.get(symbolID);
      if(!cfg?.nodes?.length) {
        stage.boundary='Control flow unavailable for '+(owner?.name||symbolID)+'. The operation remains an explicit boundary.';
        limitations.add(stage.boundary);
        return null;
      }
      const ancestry=new Set(stack);ancestry.add(symbolID);
      const sourceNodes=cfg.nodes.filter(n=>n.reachable!==false),ids=new Map(),starts=new Map(),ends=new Map();
      const original=new Map(sourceNodes.map(n=>[n.id,n]));
      for(const text of cfg.limitations||[])limitations.add(text);
      for(const a of ownAlternatives(symbolID))if(!stage.alternatives.some(other=>other.id===a.id))stage.alternatives.push(a);
      for(const n of sourceNodes) {
        const hidden=make(prefix,{location:n.location,symbolID,label:n.label,kind:n.kind,activityID});
        ids.set(n.id,hidden.id);starts.set(n.id,hidden.id);ends.set(n.id,hidden.id);
        const sourceIDs=n.callIDs||[];
        const sites=[...new Set(sourceIDs)].map(id=>calls.get(id)).filter(Boolean)
          .sort((a,b)=>(a.evaluationOrder||a.location?.offset||0)-(b.evaluationOrder||b.location?.offset||0));
        const orderKnown=new Set(sourceIDs).size===sourceIDs.length&&n.callOrderKnown!==false&&
          (sites.length<=1||n.callOrderKnown===true&&sites.every(c=>c.evaluationOrder>0));
        let last=hidden.id;
        for(const call of sites) {
          const target=targetOf(call,callbacks),callee=target.symbol,signature=callableBySymbol.get(callee?.id);
          const simpleResults=signature?.results?.some(r=>r.value.type!=='error')&&signature.results.every(r=>
            /^(?:string|bool|u?int\d*|float\d*|error|time\.Time|encoding\/json\.RawMessage)$/.test(r.value.type));
          const supporting=callee&&simpleResults&&
            (signature.kind==='function'||!(signature.parameters||[]).length);
          const same=callee&&(components.get(callee.id)?.id===components.get(symbolID)?.id&&components.has(symbolID)||
            ['function','closure'].includes(callee.kind)&&callee.package===owner?.package||supporting);
          const transport=same&&signature?.kind==='function'&&(signature.parameters||[]).some(p=>
            /(?:net\/http\.ResponseWriter|\*net\/http\.Request)/.test(p.value.type));
          const mapping=same&&depth===0&&signature?.kind==='function'&&(signature.results||[]).some(r=>
            r.value.type.includes(owner?.package+'.')||
            endpoint.entryKind==='wasm'&&r.value.type.includes('.')&&r.value.type!=='error');
          const recursive=callee&&ancestry.has(callee.id);
          const scheduled=['goroutine','deferred'].includes(call.invocation);
          const unknownInvocation=!['synchronous','goroutine','deferred'].includes(call.invocation);
          if(!callee&&!visibleExternal(call))continue;
          // A multi-call expression with unknown order stays one opaque stage.
          // Its possible handoffs are evidence, not an invented sequence.
          if(!orderKnown) {
            hidden.keep=true;hidden.phase='unordered';hidden.kind='Unordered handoffs';
            hidden.label='Expression handoffs\nOrder not expanded';
            hidden.information.push(...contracts(call,callee));
            hidden.callIDs=[...hidden.callIDs||[],call.id];
            limitations.add('Expression call order is unavailable at '+owner?.name+':'+n.location?.line+'. Inspect function specifics.');
            continue;
          }
          const directActivity=options.reviewActivities&&symbolID===(options.symbolID||endpoint.handlerID)&&
            callee?.kind==='method'&&!supporting;
          const show=!same||scheduled||recursive||transport||mapping||unknownInvocation||directActivity;
          const operation=show?callStage(call,target,prefix):null;
          if(operation) {
            operation.activityID=activityID;
            if(transport){operation.kind='Request / response';operation.collapsed=true;}
            if(mapping){operation.kind='Prepare information';operation.collapsed=true;}
            if(unknownInvocation)limitations.add('Invocation timing is unavailable for '+(call.target||callee?.name)+'. This remains a collapsed boundary.');
            link(last,operation.id);last=operation.id;
            if(recursive)limitations.add('Recursive operation '+callee.name+' remains a collapsed stage.');
          }
          const expand=callee&&!transport&&!mapping&&!recursive&&!scheduled&&!unknownInvocation&&
            (same||depth<1)&&raw.size<6000&&ancestry.size<24;
          if(!expand) {
            if(call.invocation==='goroutine') {
              const {id,...background}=operation;
              const targetSymbol=backgroundTarget(callee,callbacks);
              const enclosing=[...symbols.values()].filter(s=>s.id!==callee?.id&&s.package===callee?.package&&
                s.location?.fileID===callee?.location?.fileID&&s.location.offset<callee.location.offset&&
                s.location.endOffset>=callee.location.endOffset).sort((a,b)=>
                  (a.location.endOffset-a.location.offset)-(b.location.endOffset-b.location.offset))[0];
              operation.label='Launch background work\n'+(enclosing?.name||owner?.name||'asynchronous operation');
              const work=make(prefix,{...background,keep:true,phase:'async-work',
                kind:'Background operation',label:ownerName(targetSymbol)+'\n'+(targetSymbol?.name||'Unresolved work'),
                symbolID:targetSymbol?.id,componentID:components.get(targetSymbol?.id)?.id,
                resolution:targetSymbol?.id!==callee?.id?'inferred':target.resolution,
                alternatives:targetSymbol?[...ownAlternatives(targetSymbol.id)]:[],collapsed:!!targetSymbol});
              // Keep a distinct launch site and background body; no return edge
              // connects background completion to the caller continuation.
              link(operation.id,work.id,{kind:'asynchronous',indirect:true});
            }
            if(callee&&!recursive&&!scheduled&&!same)operation.collapsed=true;
            if(raw.size>=6000||ancestry.size>=24) {
              limitations.add('Projection size limit reached; remaining operations are explicit collapsed boundaries.');
              if(operation)operation.collapsed=true;
              else {hidden.keep=true;hidden.label='Supporting flow boundary';hidden.phase='boundary';}
            }
            continue;
          }
          const childCallbacks=new Map(callbacks);
          for(const argument of typed.get(call.id)?.arguments||[]) {
            const parameter=signature?.parameters?.find(p=>p.position===argument.parameterPosition)?.value;
            if(!parameter?.objectKey||!parameter.type.startsWith('func('))continue;
            const location=argument.value.anchor?.location;
            const candidates=[...symbols.values()].filter(s=>s.kind==='closure'&&location&&
              s.location?.fileID===location.fileID&&s.location.offset===location.offset);
            const passed=callbacks.get(argument.value.objectKey);
            if(candidates.length===1||passed)childCallbacks.set(parameter.objectKey,passed||candidates[0].id);
          }
          const child=instantiate(callee.id,prefix+'/'+call.id,same?depth:depth+1,ancestry,operation||stage,childCallbacks,
            operation?.id||activityID);
          if(!child)continue;
          link(last,child.entry,{kind:operation?'enter-component':'sequence'});
          last=child.exit;
        }
        ends.set(n.id,last);
      }
      for(const e of cfg.edges||[]) {
        if(!ids.has(e.from)||!ids.has(e.to))continue;
        const decision=original.get(e.from),branch=(cfg.edges.filter(other=>other.from===e.from).length>1)&&e.label;
        const alternatives=branch?ownAlternatives(symbolID).filter(a=>a.label===e.label&&
          a.location?.fileID===decision.location?.fileID&&a.location?.offset===decision.location?.offset):[];
        const guard=branch?decision.label+' · '+e.label:'';
        link(ends.get(e.from),starts.get(e.to),{kind:e.kind,guard,alternatives});
      }
      const exit=sourceNodes.find(n=>n.kind==='exit');
      return {entry:starts.get(cfg.entry),exit:exit?ends.get(exit.id):null};
    }
    const rootSymbol=options.symbolID||endpoint.handlerID;
    if(endpoint.reason)limitations.add(endpoint.reason);
    const rootStage=make(endpoint.id,{keep:true,phase:'entry',kind:options.symbolID?'Operation flow':'Entry point',
      label:options.symbolID?ownerName(symbols.get(rootSymbol))+'\n'+symbols.get(rootSymbol)?.name:
        endpoint.patternKnown?endpoint.pattern:endpoint.patternExpression,
      symbolID:rootSymbol,componentID:components.get(rootSymbol)?.id,
      location:symbols.get(rootSymbol)?.location,alternatives:[...ownAlternatives(rootSymbol)]});
    const body=instantiate(rootSymbol,endpoint.id,0,new Set(),rootStage);
    if(body) {
      link(rootStage.id,body.entry);
      const finish=make(endpoint.id,{keep:true,phase:'completion',kind:'Outcome',
        label:options.symbolID?'Operation returns':['startup','main','initialization'].includes(endpoint.entryKind)?'Startup operation completes':
          endpoint.entryKind==='wasm'?'Wasm export returns':endpoint.entryKind==='library'?'Public API returns':'HTTP handler completes'});
      link(body.exit,finish.id);
    }else rootStage.boundary=endpoint.reason||'Entry implementation unavailable. Declaration evidence remains inspectable.';

    // Contract hidden CFG regions. Guard sets describe alternatives encountered
    // on possible paths, never a conjunction or proof of edge execution.
    const outgoing=new Map();
    for(const e of links) {
      if(!outgoing.has(e.from))outgoing.set(e.from,[]);
      outgoing.get(e.from).push(e);
    }
    const visible=[...raw.values()].filter(n=>n.keep),edges=[];
    for(const source of visible) {
      const initial=source.id+':false:control',pending=[initial];
      const seen=new Map([[initial,{id:source.id,indirect:false,kind:'control',incoming:[]}]]);
      const destinations=new Map();
      for(let index=0;index<pending.length;index++) {
        const key=pending[index];
        const state=seen.get(key);
        for(const e of outgoing.get(state.id)||[]) {
          const indirect=state.indirect||!!e.indirect;
          const kind=e.kind==='asynchronous'?'asynchronous':state.kind;
          const transition={from:key,edge:e};
          if(raw.get(e.to)?.keep) {
            const key=e.to+':'+kind;
            if(!destinations.has(key))destinations.set(key,{from:source.id,to:e.to,kind,
              guards:new Set(),alternatives:new Map(),indirect,incoming:[]});
            const edge=destinations.get(key);
            edge.indirect ||= indirect;
            edge.incoming.push(transition);
          }else {
            const key=e.to+':'+indirect+':'+kind;
            if(!seen.has(key)) {
              seen.set(key,{id:e.to,indirect,kind,incoming:[]});pending.push(key);
            }
            seen.get(key).incoming.push(transition);
          }
        }
      }
      for(const destination of destinations.values()) {
        // Every state is source-reachable. Reverse reachability from one visible
        // destination identifies exactly the hidden edges on its possible paths,
        // including loops, without repeatedly copying growing guard unions.
        const work=[...destination.incoming],visited=new Set();
        for(let i=0;i<work.length;i++) {
          const {from,edge}=work[i];
          if(edge.guard)destination.guards.add(edge.guard);
          for(const a of edge.alternatives||[])destination.alternatives.set(a.id,a);
          if(visited.has(from))continue;
          visited.add(from);work.push(...seen.get(from).incoming);
        }
        const {incoming,...e}=destination;
        const guards=[...e.guards],label=e.kind==='asynchronous'?'async work':
          guards.length===1?guards[0]:guards.length?guards.length+' source alternatives':'';
        edges.push({...e,guards,alternatives:[...e.alternatives.values()],architecture:true,
          label:label.length>65?label.slice(0,62)+'...':label,
          description:guards.join('\n')||'Possible control continuation'});
      }
    }
    const reachable=new Set([rootStage.id]),queue=[rootStage.id];
    const edgeMap=new Map();
    for(const e of edges){if(!edgeMap.has(e.from))edgeMap.set(e.from,[]);edgeMap.get(e.from).push(e.to);}
    for(let i=0;i<queue.length;i++)for(const id of edgeMap.get(queue[i])||[])if(!reachable.has(id)){reachable.add(id);queue.push(id);}
    const nodes=visible.filter(n=>reachable.has(n.id));
    for(const n of nodes) {
      const brief=direction=>[...new Set(n.information.filter(i=>i.direction===direction&&
        !['context.Context','net/http.ResponseWriter','error','bool'].includes(i.type)).map(i=>
        i.type.split('/').at(-1)))].slice(0,2).join(', ');
      const input=brief('input'),output=brief('output');
      if(input||output)n.label+='\n'+(input||'input')+' → '+(output||'outcome');
      const states=new Set(n.alternatives.map(a=>a.execution));
      n.status=n.boundary||((states.size>1?'mixed':states.values().next().value||'unknown')+' alternatives')+
        (n.resolution==='inferred'?' · inferred target':n.resolution==='unresolved'?' · unresolved':'');
    }
    return {entry:rootStage.id,nodes,edges:edges.filter(e=>reachable.has(e.from)&&reachable.has(e.to)),
      alternatives:[...new Map(nodes.flatMap(n=>n.alternatives).map(a=>[a.id,a])).values()],limitations:[...limitations]};
  }
  function activityOverview(report,scene) {
    if(scene.error)return scene;
    const symbols=new Map((report.symbols||[]).map(s=>[s.id,s]));
    const signatures=new Map((report.operationSources?.callables||[]).map(c=>[c.symbolID,c]));
    const byID=new Map(scene.nodes.map(n=>[n.id,n])),owners=new Map();
    for(const node of scene.nodes) {
      let owner=node;
      if(node.phase!=='async-work') {
        const seen=new Set([node.id]);
        while(owner.activityID&&owner.activityID!==scene.entry&&byID.has(owner.activityID)&&!seen.has(owner.activityID)) {
          seen.add(owner.activityID);owner=byID.get(owner.activityID);
        }
      }
      owners.set(node.id,owner.id);
    }
    const nodes=scene.nodes.filter(n=>owners.get(n.id)===n.id).map(n=>{
      const symbol=symbols.get(n.symbolID);
      const name=symbol?.name.replace(/([a-z0-9])([A-Z])/g,'$1 $2')||n.label.split('\n')[0];
      let description=symbol?.doc||'';
      const substitutions=new Map(),outputs=n.information.filter(i=>i.direction==='output');
      for(const [index,result] of (signatures.get(n.symbolID)?.results||[]).entries())
        if(/^[A-Z]\w*$/.test(result.value.type)&&outputs[index]?.type)
          substitutions.set(result.value.type,outputs[index].type);
      description=description.split(/\b/).map(word=>substitutions.get(word)||word).join('');
      const phrase=description.split(/\n\s*\n/)[0].trim();
      const title=n.phase==='entry'?n.label.split('\n')[0]:
        phrase?phrase:
          (n.componentID?n.label.split('\n')[0]+' · ':'')+name;
      return {...n,title:title[0]?.toUpperCase()+title.slice(1),description,operations:[],internalEdges:[],
        alternatives:[...n.alternatives]};
    });
    const activities=new Map(nodes.map(n=>[n.id,n]));
    for(const node of scene.nodes)if(owners.get(node.id)!==node.id)
      activities.get(owners.get(node.id)).operations.push(node);
    const edges=new Map();
    for(const edge of scene.edges) {
      const from=owners.get(edge.from),to=owners.get(edge.to);
      if(from===to){activities.get(from).internalEdges.push(edge);continue;}
      const key=from+':'+to+':'+edge.kind;
      if(!edges.has(key))edges.set(key,{from,to,kind:edge.kind,segments:[],guards:[],alternatives:[]});
      edges.get(key).segments.push(edge);
    }
    for(const edge of edges.values()) {
      edge.guards=[...new Set(edge.segments.flatMap(e=>e.guards||[]))];
      edge.alternatives=[...new Map(edge.segments.flatMap(e=>e.alternatives||[]).map(a=>[a.id,a])).values()];
    }
    for(const n of nodes)n.alternatives=[...new Map([n,...n.operations].flatMap(o=>o.alternatives)
      .concat(n.internalEdges.flatMap(e=>e.alternatives||[])).map(a=>[a.id,a])).values()];
    return {...scene,nodes,edges:[...edges.values()]};
  }
  const api={endpointFlow,activityOverview};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;
  else root.GoReviewEndpointFlow=api;
})(typeof globalThis==='undefined'?this:globalThis);
