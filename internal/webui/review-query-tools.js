(function(root) {
  'use strict';
  const common=typeof module!=='undefined'&&module.exports;
  const load=(name,key)=>common?require('./'+name+'-tools.js'):root[key];
  const application=load('application','GoReviewApplication');
  const endpoint=load('endpoint-flow','GoReviewEndpointFlow');
  const visual=load('visual-review','GoReviewVisual');
  const assessment=load('assessment','GoReviewAssessment');
  const system=load('system-flow','GoReviewSystemFlow');
  const documentation=load('documentation','GoReviewDocumentation');
  const reviewMap=load('review','GoReviewMap');
  const dataFlow=load('dataflow','GoReviewDataFlow');
  const logical=load('logical','GoReviewLogical');

  const maxResultBytes=32768,pageBytes=6144;
  function byteLength(text) {
    let size=0;
    for(const c of text) {const n=c.codePointAt(0);size+=n<128?1:n<2048?2:n<65536?3:4;}
    return size;
  }
  function page(items,args={}) {
    const offset=args.offset??0,limit=args.limit??10;
    if(!Number.isInteger(offset)||offset<0)throw Error('offset must be a nonnegative integer');
    if(!Number.isInteger(limit)||limit<1||limit>100)throw Error('limit must be between 1 and 100');
    const selected=[];let size=2;
    for(const item of items.slice(offset,offset+limit)) {
      const bytes=byteLength(JSON.stringify(item))+1;
      if(size+bytes>pageBytes) {
        if(!selected.length)throw Error('One detail exceeds the 6 KiB page budget; select a narrower section or read its retained source/text range.');
        break;
      }
      selected.push(item);size+=bytes;
    }
    return {items:selected,total:items.length,offset,
      nextOffset:offset+selected.length<items.length?offset+selected.length:null};
  }
  function textResult(value,key,args) {
    const text=value[key]||'',offset=args.textOffset??0,limit=args.textLimit??12000;
    if(!Number.isInteger(offset)||offset<0||!Number.isInteger(limit)||limit<1||limit>12000)
      throw Error('textOffset must be nonnegative and textLimit must be 1-12000 Unicode characters');
    const chars=Array.from(text);
    if(!offset&&chars.length<=limit&&byteLength(JSON.stringify(text))<=16384)return value;
    let end=offset,size=0;
    while(end<chars.length&&end-offset<limit) {
      const bytes=byteLength(JSON.stringify(chars[end]))-2;
      if(size+bytes>16384)break;
      size+=bytes;end++;
    }
    return {...value,[key]:chars.slice(offset,end).join(''),
      textPage:{field:key,offset,total:chars.length,nextOffset:end<chars.length?end:null}};
  }
  function metrics(report,ids) {
    const {symbolIDs,...summary}=visual.metrics(report,ids),owners=summary.complexity.maximumSymbolIDs;
    return {...summary,complexity:{...summary.complexity,maximumOwnerCount:owners.length,
      maximumSymbolIDs:page(owners,{limit:10}).items}};
  }
  const symbolSummary=s=>({id:s.id,name:s.name,qualified:s.qualified,kind:s.kind,
    package:s.package,signature:s.signature,cyclomatic:s.cyclomatic,location:s.location,
    docLength:s.doc?.length||0,commentCount:s.comments?.length||0});
  function comments(comments,args) {
    if(args.commentIndex!==undefined) {
      if(!Number.isInteger(args.commentIndex)||args.commentIndex<0||!comments[args.commentIndex])
        throw Error('Unknown retained comment: '+args.commentIndex);
      return textResult(comments[args.commentIndex],'text',args);
    }
    return page(comments.map(({text,...comment},index)=>({...comment,index,textLength:Array.from(text).length})),args);
  }
  function packageSummary(report,p) {
    const ids=(report.symbols||[]).filter(s=>s.package===p.path).map(s=>s.id);
    return {path:p.path,name:p.name,symbolCount:ids.length,fileCount:p.fileIDs?.length||0,
      importCount:p.imports?.length||0,typeErrorCount:p.typeErrors?.length||0,docLength:p.doc?.length||0,
      metrics:metrics(report,ids)};
  }
  function collection(items,args) {
    const {items:values,...paging}=page(items,args);
    return {values,paging};
  }
  function scopeSummary(scope={}) {
    return {root:scope.root,verifyRepository:scope.verifyRepository,
      excludedPathCount:scope.excludePaths?.length||0,excludedTestCount:scope.excludeTests?.length||0,
      unitSelectionCount:scope.unitSelections?.length||0};
  }
  const scopeSections=report=>({excludedPaths:()=>report.scope?.excludePaths||[],
    excludedTests:()=>report.scope?.excludeTests||[],unitSelections:()=>report.scope?.unitSelections||[]});
  function symbol(report,id) {
    const found=(report.symbols||[]).find(s=>s.id===id);
    if(!found)throw Error('Unknown symbol: '+id);
    return found;
  }
  function entry(report,id) {
    const route=application.entryByID(report,id);
    if(route)return route;
    throw Error('Unknown entry: '+id);
  }
  // Both browser and MCP use this scene: occurrence IDs, hierarchy, edge
  // contraction and metric membership are never independently reconstructed.
  function rawScene(report,selected,symbolID) {
    return endpoint.endpointFlow(report,selected,{reviewActivities:true,symbolID});
  }
  function scene(report,args={}) {
    const selected=entry(report,args.entryID);
    const build=id=>visual.hierarchy(report,rawScene(report,selected,id));
    let parent=null,tree=build();
    if(tree.error)throw Error(tree.error);
    if(args.focusSymbolID) {
      // Reconstruct successive focus levels, so deep focus cannot silently
      // jump to an unrelated symbol elsewhere in the snapshot.
      for(const id of [...(args.focusPath||[]),args.focusSymbolID]) {
        if(![...tree.byID.values()].some(n=>n.symbolID===id))
          throw Error('Focus symbol is outside this flow: '+id);
        const projected=visual.project(tree,[]);
        parent={nodes:projected.nodes.map(n=>({id:n.id,title:n.title,symbolID:n.symbolID})),
          edges:projected.edges.map(e=>({from:e.from,to:e.to,kind:e.kind}))};
        tree=build(id);
        if(tree.error)throw Error(tree.error);
      }
    } else if(args.focusPath?.length)throw Error('focusPath requires focusSymbolID');
    const expanded=args.expandedIDs||[];
    for(const id of expanded) {
      const node=tree.byID.get(id);
      if(!node)throw Error('Unknown expanded node: '+id);
      if(!node.children.length)throw Error('Expanded node has no children in this view: '+id+
        '. Expand an occurrence with childIDs; do not focus into it for in-place expansion.');
    }
    return {entry:selected,tree,projection:visual.project(tree,expanded),parent};
  }
  const nodeSummary=(report,n)=>({
    id:n.id,title:n.title,descriptionLength:n.description?.length||0,symbolID:n.symbolID,
    parentID:n.parentID,phase:n.phase,kind:n.kind,expanded:n.expanded,
    childIDs:page(n.children,{limit:10}).items,childCount:n.children.length,
    sourceCount:n.sourceIDs.length,
    location:n.location,boundary:n.boundary,
    resolution:n.resolution,invocation:n.invocation,status:n.status,
    alternativeCount:n.alternatives.length,metrics:metrics(report,n.symbolIDs)
  });
  const edgeSummary=e=>({from:e.from,to:e.to,kind:e.kind,guardCount:e.guards?.length||0,
    alternativeCount:e.alternatives.length});
  const commandSummary=(command,index)=>({
    index,executable:command.executable,args:command.args,directory:command.directory,
    startedAt:command.startedAt,finishedAt:command.finishedAt,exitCode:command.exitCode,
    stdoutSHA256:command.stdoutSHA256,stdoutLength:command.stdout?.length||0,stderrLength:command.stderr?.length||0
  });
  function runSummary(run,index) {
    const outcomeCounts={};
    for(const status of Object.values(run.outcomes||{}))outcomeCounts[status]=(outcomeCounts[status]||0)+1;
    return {index,selection:run.selection,status:run.status,error:run.error,
      matchedTestCount:run.matchedTests?.length||0,outcomeCounts,
      commandCount:run.commands?.length||0,hasRuntime:!!run.runtime};
  }
  function assessmentSummary(report,args) {
    const {checks,findings,packages,...summary}=assessment.model(report);
    return {...summary,checkCount:checks.length,findingCount:findings.length,packageCount:packages.length};
  }

  function result(report,operation,args,repository) {
    if(operation==='assess') {
      if(args.package&&!(report.packages||[]).some(p=>p.path===args.package))throw Error('Unknown assessment package');
      if(args.coverage&&!['all','known','unknown'].includes(args.coverage))throw Error('Unknown coverage filter');
      if(args.minComplexity!==undefined&&(!Number.isFinite(args.minComplexity)||args.minComplexity<0))throw Error('Invalid minimum complexity');
      if(args.maxCoverage!==undefined&&(!Number.isFinite(args.maxCoverage)||args.maxCoverage<0||args.maxCoverage>100))throw Error('Invalid maximum coverage');
      const m=assessment.model(report,args.package),records=reviewMap.reviewRecords(report);
      const plot=reviewMap.reviewPlot(records.records,args);
      if(args.symbolID&&!plot.records.some(r=>r.symbol.id===args.symbolID))throw Error('Selected function is outside the filtered Review Map');
      const rules=m.valid?report.assessment.rules||[]:[];
      if(args.ruleID) {
        const rule=rules.find(rule=>rule.id===args.ruleID);
        if(!rule)throw Error('Unknown retained rule');
        return textResult(rule,'description',args);
      }
      if(args.findingID) {
        const finding=m.findings.find(f=>f.id===args.findingID);
        if(!finding)throw Error('Unknown retained finding');
        return textResult(finding,'evidence',args);
      }
      const cohort=args.cohort?plot.groups.find(g=>g.key===args.cohort):null;
      if(args.cohort&&!cohort)throw Error('Unknown review-map cohort for these filters');
      if(args.symbolID&&cohort&&!cohort.members.some(r=>r.symbol.id===args.symbolID))throw Error('Selected function is outside the chosen cohort');
      const recordSummary=r=>({symbol:symbolSummary(r.symbol),complexity:r.complexity,coverage:r.coverage,
        architecture:assessment.indicators(report,{symbolID:r.symbol.id})});
      const sections={
        records:()=>[...(cohort?cohort.members:plot.records)].sort(reviewMap.compareRecords).map(recordSummary),
        cohorts:()=>plot.groups.map(g=>({key:g.key,count:g.members.length,
          complexity:g.members[0].complexity,coverage:g.members[0].coverage.value})),
        rules:()=>rules.map(({description,...r})=>({...r,descriptionLength:description.length})),
        findings:()=>m.findings,packages:()=>m.packages,
        checks:()=>m.checks.map(({detail,locations,...c})=>({...c,detailLength:detail?.length||0,locationCount:locations?.length||0}))};
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown assessment section');
        return page(sections[args.section](),args);
      }
      return {assessment:assessmentSummary(report,args),filteredFunctionCount:plot.records.length,
        cohortCount:plot.groups.length,unmeasuredFunctionCount:records.unmeasured,
        selected:args.symbolID?recordSummary(plot.records.find(r=>r.symbol.id===args.symbolID)):null,
        records:page(sections.records(),args),sections:Object.keys(sections)};
    }
    if(operation==='dataflow') {
      if(!['function','method','closure'].includes(symbol(report,args.symbolID).kind))throw Error('Value data flow requires a function');
      if(args.direction&&!['both','upstream','downstream'].includes(args.direction))throw Error('Invalid data-flow direction');
      if(args.depth!==undefined&&(!Number.isInteger(args.depth)||args.depth<0||args.depth>100))throw Error('Data-flow depth must be 0-100');
      if(!report.dataFlows) {
        if(args.valueID)throw Error('No retained value ID in this older review');
        return {status:'not-collected',reason:'Value data flow was not collected in this older review. Run a new explicit review.'};
      }
      if(report.dataFlows.sourceSHA256!==report.sourceSHA256)throw Error('Data-flow evidence does not match snapshot');
      const found=report.dataFlows.results.find(r=>r.symbolID===args.symbolID);
      if(args.valueID&&found?.status!=='available')throw Error('Value focus is unavailable without a retained data-flow graph');
      if(!found)return {status:'unsupported',reason:'No retained function result for this symbol'};
      if(found.status!=='available')return {status:found.status,reason:found.error};
      const graph=found.graph;
      if(!graph||graph.symbolID!==args.symbolID||graph.sourceSHA256!==report.sourceSHA256)throw Error('Invalid retained data-flow graph identity');
      if(args.valueID&&!graph.nodes.some(n=>n.id===args.valueID))throw Error('Unknown retained value ID');
      const selected=args.valueID?dataFlow.slice(graph,args.valueID,args.direction||'both',args.depth??Infinity):graph;
      const sections={nodes:selected.nodes,edges:selected.edges,limitations:graph.limitations||[]};
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown data-flow section');
        return page(sections[args.section],args);
      }
      const ns=collection(selected.nodes,args),es=collection(selected.edges,args);
      return {status:'available',symbolID:graph.symbolID,sourceSHA256:graph.sourceSHA256,
        collector:graph.collector,toolVersion:graph.toolVersion,generatedAt:graph.generatedAt,
        totalNodes:graph.nodes.length,totalEdges:graph.edges.length,
        nodes:ns.values,nodePage:ns.paging,edges:es.values,edgePage:es.paging,
        limitations:page(graph.limitations||[],args),sections:Object.keys(sections)};
    }
    if(operation==='results') {
      if(!repository)return {status:'not-collected',reason:'No repository-wide run is associated with this snapshot.'};
      const steps=repository.steps||[],modules=repository.modules||[];
      if(args.stepID) {
        const step=steps.find(s=>s.id===args.stepID);
        if(!step)throw Error('Unknown retained review step');
        if(args.commandIndex!==undefined) {
          const command=step.commands?.[args.commandIndex];
          if(!Number.isInteger(args.commandIndex)||args.commandIndex<0||!command)throw Error('Unknown review-step command');
          if(['stdout','stderr'].includes(args.section))return textResult({[args.section]:command[args.section]||''},args.section,args);
          if(args.section)throw Error('Unknown command section');
          return commandSummary(command,args.commandIndex);
        }
        if(args.section==='detail')return textResult({id:step.id,detail:step.detail||''},'detail',args);
        if(args.section==='commands')return page((step.commands||[]).map(commandSummary),args);
        if(args.section)throw Error('Unknown step section');
        return {id:step.id,title:step.title,status:step.status,module:step.module,detailLength:step.detail?.length||0,
          commandCount:step.commands?.length||0,sections:['detail','commands']};
      }
      if(args.commandIndex!==undefined)throw Error('Select stepID for a repository command');
      const sections={modules:()=>modules,steps:()=>steps.map(({commands,detail,inputs,...s})=>({...s,detailLength:detail?.length||0,commandCount:commands?.length||0})),
        exclusions:()=>repository.exclusions||[],excludedPaths:()=>repository.scope?.excludePaths||[],
        excludedTests:()=>repository.scope?.excludeTests||[],unitSelections:()=>repository.scope?.unitSelections||[]};
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown repository results section');
        return page(sections[args.section](),args);
      }
      return {root:repository.root,directory:repository.directory,status:repository.status,finishedAt:repository.finishedAt,
        moduleCount:modules.length,stepCount:steps.length,scope:scopeSummary(repository.scope),
        modules:page(modules,args),steps:page(sections.steps(),args),sections:Object.keys(sections)};
    }
    if(operation==='overview') {
      const purposeDocument=documentation.overviewDocument(report);
      const entries=application.entryInventory(report,{query:args.query});
      const groups=new Map();
      for(const e of entries)groups.set(e.group,(groups.get(e.group)||0)+1);
      const startup=entries.filter(e=>e.entryKind==='main').map(e=>({id:e.id,symbolID:e.handlerID,
        name:e.title,location:e.location,metrics:metrics(report,[e.handlerID])}));
      const initialization=application.initializationInventory(report).map(e=>({
        id:e.id,handlerID:e.handlerID,name:e.title,location:e.location,metrics:metrics(report,[e.handlerID])}));
      const sections={
        entries:()=>entries.map(e=>({id:e.id,method:e.method,pattern:e.pattern,handlerID:e.handlerID,
          kind:e.entryKind,group:e.group,location:e.location,resolution:e.resolution,reason:e.reason,
          world:e.world,interface:e.interface,function:e.function,evidenceCount:e.evidence?.length||0,
          metrics:metrics(report,e.handlerID?[e.handlerID]:[])})),
        initialization:()=>initialization,
        entrySources:()=>(report.entryPoints?.sources||[]).map(({id,path,sha256})=>({id,path,sha256})),
        entryLimitations:()=>report.entryPoints?.limitations||[],
        startup:()=>startup,groups:()=>[...groups].map(([group,count])=>({group,count})),
        documents:()=>(report.documents||[]).map(({id,path,sha256})=>({id,path,sha256})),
        exclusions:()=>report.exclusions||[],warnings:()=>report.warnings||[],limitations:()=>report.limitations||[],
        maximumOwners:()=>visual.metrics(report,(report.symbols||[]).map(s=>s.id)).complexity.maximumSymbolIDs,
        ...scopeSections(report)};
      if(args.section==='entryEvidence')return page(entry(report,args.entryID).evidence||[],args);
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown overview section: '+args.section);
        return page(sections[args.section](),args);
      }
      const starts=collection(startup,args);
      return {module:report.module,root:report.root,revision:report.revision,
        projectKind:report.entryPoints?.kind||'unclassified',entryDiscoveryStatus:report.entryPoints?'collected':'legacy',
        entryDiscoveryNote:report.entryPoints?null:'This older snapshot lacks WIT/configuration and public API entry evidence. Analyze again for complete entry discovery.',
        initializationCount:initialization.length,entrySourceCount:report.entryPoints?.sources?.length||0,
        entryLimitations:page(report.entryPoints?.limitations||[],args),
        purposeAvailable:!!(report.packages||[]).find(p=>p.path===report.module)?.doc,
        purposeDocument:purposeDocument?{id:purposeDocument.id,path:purposeDocument.path,sha256:purposeDocument.sha256}:null,
        groups:page(sections.groups(),args),entries:page(sections.entries(),args),
        startup:starts.values,startupPage:starts.paging,
        documents:page(sections.documents(),args),
        metrics:metrics(report,(report.symbols||[]).map(s=>s.id)),
        packageCount:(report.packages||[]).length,symbolCount:(report.symbols||[]).length,
        assessment:assessmentSummary(report,args),scope:scopeSummary(report.scope),
        exclusions:page(sections.exclusions(),args),warnings:page(sections.warnings(),args),
        limitations:page(sections.limitations(),args),
        sections:[...Object.keys(sections),'entryEvidence']};
    }
    if(operation==='flow'||operation==='inspect') {
      const {tree,projection:p,parent,entry:selected}=scene(report,args);
      if(operation==='flow') {
        const nodes=p.nodes.map(n=>nodeSummary(report,n)),edges=p.edges.map(edgeSummary);
        const sections={nodes,edges,parentNodes:parent?.nodes||[],parentEdges:parent?.edges||[],limitations:p.limitations||[]};
        if(args.section) {
          if(!sections[args.section])throw Error('Unknown flow section: '+args.section);
          return page(sections[args.section],args);
        }
        const ns=collection(nodes,args),es=collection(edges,args);
        return {context:{entryID:args.entryID,focusSymbolID:args.focusSymbolID,
          focusPath:args.focusPath||[],expandedIDs:args.expandedIDs||[]},
          entry:{id:selected.id,handlerID:selected.handlerID,pattern:selected.pattern,entryKind:selected.entryKind},
          nodes:ns.values,nodePage:ns.paging,edges:es.values,edgePage:es.paging,
          parent:parent?{nodeCount:parent.nodes.length,edgeCount:parent.edges.length}:null,
          limitations:page(p.limitations||[],args),sections:Object.keys(sections)};
      }
      if(args.nodeID) {
        const n=p.nodes.find(n=>n.id===args.nodeID);
        if(!n)throw Error('Unknown or hidden node in this context: '+args.nodeID);
        const s=n.symbolID?symbol(report,n.symbolID):null;
        const sections={
          members:()=>n.symbolIDs,maximumOwners:()=>visual.metrics(report,n.symbolIDs).complexity.maximumSymbolIDs,
          children:()=>n.children.map(id=>nodeSummary(report,tree.byID.get(id))),
          operations:()=>n.sourceIDs.filter(id=>id!==n.id).map(id=>nodeSummary(report,tree.byID.get(id))),
          alternatives:()=>n.alternatives,information:()=>n.information||[],
          findings:()=>s?assessment.contextFindings(report,{symbolID:s.id}):[]};
        if(args.section==='documentation')return textResult({symbolID:s?.id,text:n.description||''},'text',args);
        if(args.section==='comments')return comments(s?.comments||[],args);
        if(args.section) {
          if(!sections[args.section])throw Error('Unknown node inspection section: '+args.section);
          return page(sections[args.section](),args);
        }
        return {node:nodeSummary(report,n),symbol:s?symbolSummary(s):null,metrics:metrics(report,n.symbolIDs),
          operations:page(n.sourceIDs.filter(id=>id!==n.id).map(id=>nodeSummary(report,tree.byID.get(id))),args),
          alternatives:page(n.alternatives,args),
          continuations:page(p.edges.filter(e=>e.from===n.id||e.to===n.id).map(edgeSummary),args),
          findingCount:sections.findings().length,commentCount:s?.comments?.length||0,
          limitations:page(p.limitations||[],args),sections:[...Object.keys(sections),'documentation','comments']};
      }
      const e=p.edges.find(e=>e.from===args.from&&e.to===args.to&&e.kind===(args.kind||'control'));
      if(!e)throw Error('Unknown edge in this context');
      if(args.section==='guards')return page(e.guards||[],args);
      if(args.section&&args.section!=='alternatives')throw Error('Unknown edge inspection section: '+args.section);
      return {...edgeSummary(e),alternatives:page(e.alternatives,args),
        limitations:page(p.limitations||[],args),sections:['alternatives','guards']};
    }
    if(operation==='document') {
      const d=[...(report.documents||[]),...(report.entryPoints?.sources||[])].find(d=>d.id===args.id);
      if(!d)throw Error('Unknown document: '+args.id);
      return textResult(d,'text',args);
    }
    if(operation==='source') {
      const file=(report.files||[]).concat(report.supportingFiles||[],(report.testSources||[]).map(t=>t.file)).find(f=>f.id===args.fileID);
      if(!file)throw Error('Unknown retained file ID: '+args.fileID);
      const lines=file.source.split('\n'),start=args.startLine??1,end=args.endLine??Math.min(lines.length,start+99);
      if(!Number.isInteger(start)||!Number.isInteger(end)||start<1||end<start||end>lines.length||end-start>=500)
        throw Error('Source range '+start+'-'+end+' is invalid for '+file.path+
          ' ('+lines.length+' retained lines; request 1-500 valid lines)');
      return textResult({fileID:file.id,path:file.path,sha256:file.sha256,startLine:start,endLine:end,
        totalLines:lines.length,text:lines.slice(start-1,end).join('\n')},'text',args);
    }
    if(operation==='search') {
      const q=args.query?.trim().toLowerCase();
      if(!q)throw Error('A nonempty query is required');
      const items=[
        ...(report.symbols||[]).filter(s=>[s.name,s.package,s.signature,s.doc,
          ...(s.comments||[]).map(c=>c.text)].join('\n').toLowerCase().includes(q))
          .map(s=>({kind:'symbol',id:s.id,name:s.qualified||s.name,package:s.package,location:s.location})),
        ...[...(report.documents||[]),...(report.entryPoints?.sources||[])].filter(d=>(d.path+'\n'+d.text).toLowerCase().includes(q))
          .map(d=>({kind:'document',id:d.id,path:d.path})),
        ...(report.testSources||[]).flatMap(t=>(t.declarations||[]).filter(d=>
          (d.name+'\n'+t.file.source).toLowerCase().includes(q)).map(d=>
          ({kind:'test',id:d.id,name:d.name,location:d.location})))];
      return page(items,args);
    }
    if(operation==='logical') {
      const owner=(report.symbols||[]).find(s=>s.id===args.symbolID);
      if(!owner)throw Error('Select retained symbolID for logical relationships');
      const model=logical.logicalRelationships(report,owner.id);
      const groups=['crossPackage','samePackage','references','callers'];
      const sections={};
      for(const key of groups)sections[key]=model[key].map(g=>({symbol:symbolSummary(g.symbol),callCount:g.calls.length,referenceCount:g.references.length}));
      sections.boundaries=model.boundaries;sections.conversions=model.conversions;
      if(args.section==='sites') {
        const group=groups.flatMap(key=>model[key]).find(g=>g.symbol.id===args.nodeID);
        if(!group)throw Error('Select a related symbol as nodeID');
        return page([...group.calls.map(c=>({...c,relationship:'source-call'})),...group.references.map(r=>({...r,relationship:'source-reference'}))],args);
      }
      if(args.section){if(!sections[args.section])throw Error('Unknown logical relationship section');return page(sections[args.section],args);}
      return {symbol:symbolSummary(owner),groups:Object.fromEntries(Object.entries(sections).map(([key,values])=>[key,page(values,args)])),
        sections:[...Object.keys(sections),'sites'],limitations:['Static local calls and function-value references; references do not establish invocation.']};
    }
    if(operation==='packages') {
      if(!args.package) {
        if(args.section)throw Error('Select an exact package before requesting a package section');
        return page((report.packages||[]).map(p=>packageSummary(report,p)),args);
      }
      const p=(report.packages||[]).find(p=>p.path===args.package);
      if(!p)throw Error('Unknown package: '+args.package);
      const symbols=(report.symbols||[]).filter(s=>s.package===p.path);
      const sections={symbols:()=>symbols.map(symbolSummary),imports:()=>p.imports||[],
        files:()=>(report.files||[]).filter(f=>f.package===p.path).map(({id,path,sha256})=>({id,path,sha256})),
        typeErrors:()=>p.typeErrors||[],maximumOwners:()=>visual.metrics(report,symbols.map(s=>s.id)).complexity.maximumSymbolIDs};
      if(args.section==='documentation')return textResult({package:p.path,text:p.doc||''},'text',args);
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown package section: '+args.section);
        return page(sections[args.section](),args);
      }
      return {package:packageSummary(report,p),symbols:page(symbols.map(symbolSummary),args),
        sections:[...Object.keys(sections),'documentation']};
    }
    if(operation==='architecture') {
      if(args.level&&!['context','components'].includes(args.level))throw Error('Unknown architecture level');
      if(args.componentID&&!(report.systemFlow?.components||[]).some(c=>c.id===args.componentID))
        throw Error('Unknown component: '+args.componentID);
      if(args.operationID&&!(report.systemFlow?.operations||[]).some(o=>o.id===args.operationID))
        throw Error('Unknown operation: '+args.operationID);
      const model=system.project(report,args);
      if(model.error)throw Error(model.error);
      const nodes=model.nodes.map(n=>({id:n.id,name:n.name,kind:n.kind,group:n.group,
        execution:n.execution,memberCount:n.members.length,sourceCount:n.sources.length,
        alternativeCount:n.alternatives.length}));
      const edges=model.edges.map(e=>({id:e.id,from:e.from,to:e.to,kind:e.kind,direction:e.direction,
        kinds:e.kinds,resolution:e.resolution,exchangeCount:e.exchangeIDs.length,informationCount:e.information.length}));
      const components=(report.systemFlow.components||[]).filter(c=>!args.componentID||c.id===args.componentID);
      const operations=(report.systemFlow.operations||[]).filter(o=>!args.componentID||o.componentID===args.componentID);
      if(args.section==='documentation') {
        const c=components.find(c=>c.id===args.componentID);
        if(!c)throw Error('Select componentID for documentation');
        return textResult({componentID:c.id,text:c.doc||''},'text',args);
      }
      if(args.section==='operation') {
        const op=operations.find(o=>o.id===args.operationID);
        if(!op)throw Error('Select operationID for operation detail');
        return op;
      }
      if(['exchangeSources','exchangeCalls','exchangeInformation'].includes(args.section)) {
        const e=model.exchanges.find(e=>e.id===args.exchangeID);
        if(!e)throw Error('Select exchangeID for exchange detail');
        return page((args.section==='exchangeSources'?e.sources:args.section==='exchangeCalls'?e.callIDs:e.information)||[],args);
      }
      if(['alternativeMembers','alternativeWitnesses','alternativeRequires'].includes(args.section)) {
        const a=model.alternatives.find(a=>a.id===args.alternativeID);
        if(!a)throw Error('Select alternativeID for alternative detail');
        return page((args.section==='alternativeMembers'?a.members:args.section==='alternativeWitnesses'?a.witnesses:a.requires)||[],args);
      }
      if(args.section==='members'||args.section==='sources') {
        const n=model.nodes.find(n=>n.id===args.nodeID);
        if(!n)throw Error('Select a visible architectural nodeID for '+args.section);
        return page(n[args.section],args);
      }
      if(args.section==='groupMembers') {
        const group=model.groups.find(g=>g.id===args.group);
        if(!group)throw Error('Select an architectural group for groupMembers');
        return page(group.members,args);
      }
      if(args.section==='edgeExchanges'||args.section==='information') {
        const e=model.edges.find(e=>e.id===args.edgeID);
        if(!e)throw Error('Select a visible architectural edgeID for '+args.section);
        return page(args.section==='edgeExchanges'?e.exchangeIDs:e.information,args);
      }
      const sections={nodes,edges,groups:model.groups.map(g=>({id:g.id,name:g.name,memberCount:g.members.length})),
        exchanges:model.exchanges.map(e=>({id:e.id,from:e.from,to:e.to,kind:e.kind,resolution:e.resolution,
          reason:e.reason,sourceCount:e.sources?.length||0,callCount:e.callIDs?.length||0,informationCount:e.information?.length||0})),
        alternatives:model.alternatives.map(({witnesses,members,requires,...a})=>({...a,
          witnessCount:witnesses?.length||0,memberCount:members?.length||0,requirementCount:requires?.length||0})),
        components:components.map(c=>({id:c.id,name:c.name,kind:c.kind,package:c.package,
          memberCount:c.members?.length||0,sourceCount:c.sources?.length||0})),
        operations:operations.map(o=>({id:o.id,name:o.name,symbolID:o.symbolID,componentID:o.componentID})),
        limitations:model.limitations};
      if(args.section==='componentMembers'||args.section==='componentSources') {
        const c=components.find(c=>c.id===args.componentID);
        if(!c)throw Error('Select componentID for component detail');
        return page((args.section==='componentMembers'?c.members:c.sources)||[],args);
      }
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown architecture section: '+args.section);
        return page(sections[args.section],args);
      }
      const ns=collection(nodes,args),es=collection(edges,args);
      return {nodes:ns.values,nodePage:ns.paging,edges:es.values,edgePage:es.paging,
        groups:page(sections.groups,args),componentCount:components.length,operationCount:operations.length,
        exchangeCount:model.exchanges.length,alternativeCount:model.alternatives.length,
        limitations:page(model.limitations,args),
        sections:[...Object.keys(sections),'members','sources','groupMembers','edgeExchanges','information','componentMembers','componentSources',
          'documentation','operation','exchangeSources','exchangeCalls','exchangeInformation',
          'alternativeMembers','alternativeWitnesses','alternativeRequires']};
    }
    if(operation==='evidence') {
      if(args.checkID!==undefined) {
        const check=assessment.model(report).checks.find(c=>c.id===args.checkID);
        if(!check)throw Error('Unknown retained check: '+args.checkID);
        if(args.section==='locations')return page(check.locations||[],args);
        if(args.section&&args.section!=='detail')throw Error('Unknown check section: '+args.section);
        const {locations,...detail}=check;
        return textResult({...detail,locationCount:locations?.length||0},'detail',args);
      }
      if(args.commandIndex!==undefined) {
        const run=args.runIndex===undefined?null:report.unitEvidence?.runs?.[args.runIndex];
        if(args.runIndex!==undefined&&(!Number.isInteger(args.runIndex)||args.runIndex<0||!run))
          throw Error('Unknown retained test run: '+args.runIndex);
        const command=(run?run.commands:report.commands)?.[args.commandIndex];
        if(!Number.isInteger(args.commandIndex)||args.commandIndex<0||!command)
          throw Error('Unknown retained command: '+args.commandIndex);
        if(args.section==='stdout'||args.section==='stderr')
          return textResult({index:args.commandIndex,[args.section]:command[args.section]||''},args.section,args);
        if(args.section)throw Error('Unknown command section: '+args.section);
        return commandSummary(command,args.commandIndex);
      }
      if(args.runIndex!==undefined) {
        const run=report.unitEvidence?.runs?.[args.runIndex];
        if(!Number.isInteger(args.runIndex)||args.runIndex<0||!run)
          throw Error('Unknown retained test run: '+args.runIndex);
        const runtime=run.runtime;
        const sections={commands:()=>(run.commands||[]).map(commandSummary),
          matchedTests:()=>run.matchedTests||[],
          outcomes:()=>Object.entries(run.outcomes||{}).map(([test,status])=>({test,status})),
          observations:()=>runtime?.observations||[],limitations:()=>runtime?.limitations||[]};
        if(args.section) {
          if(!sections[args.section])throw Error('Unknown run section: '+args.section);
          return page(sections[args.section](),args);
        }
        const testRun={...runSummary(run,args.runIndex),matchedTests:page(run.matchedTests||[],args),
          outcomes:page(Object.entries(run.outcomes||{}).map(([test,status])=>({test,status})),args)};
        if(!runtime)return {status:'not-collected',observations:page([],args),
          testRun,limitations:page(['No standard runtime evidence was retained for this run.'],args),
          sections:Object.keys(sections)};
        const {observations,...metadata}=runtime;
        return {...metadata,testRun,
          observations:page(observations||[],args),limitations:page(metadata.limitations||[],args),
          sections:Object.keys(sections)};
      }
      const s=args.symbolID?symbol(report,args.symbolID):null;
      const runs=report.unitEvidence;
      const cfg=s?(report.controlFlows||[]).find(f=>f.symbolID===s.id):null;
      if(args.functionView) {
        if(!['logical','control','calls'].includes(args.functionView))throw Error('Unknown function view');
        if(!s||!['function','method','closure'].includes(s.kind))throw Error('Function view requires a callable symbol');
        if(args.functionView==='control'&&!cfg)throw Error('Control flow was not retained for this function');
      }
      const sections={
        calls:()=>(report.calls||[]).filter(c=>s&&(c.from===s.id||c.to===s.id)),
        references:()=>(report.references||[]).filter(r=>s&&(r.to===s.id||r.from===s.id)),
        controlFlowNodes:()=>cfg?.nodes||[],controlFlowEdges:()=>cfg?.edges||[],
        controlFlowLimitations:()=>cfg?.limitations||[],comments:()=>s?.comments||[],
        tests:()=>(report.testSources||[]).flatMap(t=>(t.declarations||[]).map(d=>({...d,fileID:t.file.id,path:t.file.path}))),
        checks:()=>assessment.model(report).checks.map(({detail,locations,...check})=>({...check,
          detailLength:detail?.length||0,locationCount:locations?.length||0})),
        findings:()=>s?assessment.contextFindings(report,{symbolID:s.id}):assessment.model(report).findings,
        packages:()=>assessment.model(report).packages.map(({findings,...p})=>({...p,findingCount:findings?.length||0})),
        runs:()=>(runs?.runs||[]).map(runSummary),commands:()=>(report.commands||[]).map(commandSummary),
        exclusions:()=>report.exclusions||[],warnings:()=>report.warnings||[],limitations:()=>report.limitations||[],
        runLimitations:()=>runs?.limitations||[],...scopeSections(report)};
      if(args.section==='documentation') {
        if(!s)throw Error('Select symbolID for documentation');
        return textResult({symbolID:s.id,text:s.doc||''},'text',args);
      }
      if(args.section==='comments') {
        if(!s)throw Error('Select symbolID for comments');
        return comments(s.comments||[],args);
      }
      if(args.section) {
        if(!sections[args.section])throw Error('Unknown evidence section: '+args.section);
        if(['calls','references','comments','controlFlowNodes','controlFlowEdges'].includes(args.section)&&!s)
          throw Error('Select symbolID for '+args.section);
        return page(sections[args.section](),args);
      }
      return {symbol:s?symbolSummary(s):null,
        controlFlow:cfg?{symbolID:cfg.symbolID,entry:cfg.entry,nodeCount:cfg.nodes?.length||0,edgeCount:cfg.edges?.length||0}:null,
        callCount:sections.calls().length,referenceCount:sections.references().length,
        metrics:s?metrics(report,[s.id]):null,
        coverage:report.coverage?{mode:report.coverage.mode,origin:report.coverage.origin,
          sha256:report.coverage.sha256,sourceVerified:report.coverage.sourceVerified,
          statements:report.coverage.statements,coveredStatements:report.coverage.coveredStatements}:null,
        unitEvidence:runs?{sourceSHA256:runs.sourceSHA256,allTests:runs.allTests,
          limitationCount:runs.limitations?.length||0,runs:page((runs.runs||[]).map(runSummary),args)}:null,
        testCount:sections.tests().length,
        assessment:s?assessment.indicators(report,{symbolID:s.id}):assessmentSummary(report,args),
        scope:scopeSummary(report.scope),exclusionCount:report.exclusions?.length||0,commandCount:report.commands?.length||0,
        warningCount:report.warnings?.length||0,limitations:page(report.limitations||[],args),
        sections:[...Object.keys(sections),'documentation']};
    }
    throw Error('Unknown review query: '+operation);
  }
  function query(report,operation,args={},repository=null) {
    const value=result(report,operation,args,repository),size=byteLength(JSON.stringify(value));
    if(size>maxResultBytes)throw Error('Review '+operation+' result exceeds 32 KiB ('+size+
      ' bytes). Request a single section and a smaller limit, or use textOffset/textLimit for text. No evidence was truncated.');
    return value;
  }
  const api={query,scene,rawScene};
  if(common)module.exports=api;else root.GoReviewQuery=api;
})(typeof globalThis==='undefined'?this:globalThis);
