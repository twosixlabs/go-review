(function(root) {
  'use strict';
  const common=typeof module!=='undefined'&&module.exports;
  const query=common?require('./review-query-tools.js'):root.GoReviewQuery;
  // Only an explicit movement request authorizes automatic navigation. Other
  // phrasings still work through the agent's clickable destination proposal.
  function navigationRequested(text) {
    return /(?:^|[.!?]\s*)(?:(?:please|then)\s+)?(?:(?:can|could|would|will)\s+you\s+)?(?:please\s+)?(?:show|open|expand|collapse|focus|select|navigate|go|take me|bring me|return|switch|zoom)\b/i.test(text.trim());
  }
  function validate(report,action,snapshotID,repository=null) {
    if(!snapshotID||action.snapshotID!==snapshotID)throw Error('Stale chat snapshot; start from current evidence.');
    const operation={document:'document',symbol:'evidence',logical:'evidence',control:'evidence',calls:'evidence',dataflow:'dataflow',
      package:'packages',architecture:'architecture',flow:'flow','review-map':'assess',signals:'assess',hygiene:'evidence',
      results:'results',scope:'evidence',limits:'evidence'}[action.view];
    if(['application','packages','back'].includes(action.view))return action;
    if(!operation)throw Error('Unsupported navigation view: '+action.view);
    if(action.view==='document'&&![...(report.documents||[]),...(report.entryPoints?.sources||[])].some(d=>d.id===action.id))throw Error('Unknown document');
    if(action.view==='symbol'&&!(report.symbols||[]).some(s=>s.id===action.id))throw Error('Unknown symbol');
    if(action.view==='package'&&!(report.packages||[]).some(p=>p.path===action.id))throw Error('Unknown package');
    if(action.view==='architecture'&&!['context','components'].includes(action.id))throw Error('Unknown architecture level');
    const args=action.view==='flow'?{entryID:action.entryID,focusSymbolID:action.focusSymbolID,
      focusPath:action.focusPath||[],expandedIDs:action.expandedIDs||[],nodeID:action.nodeID,from:action.from,to:action.to,kind:action.kind}:
      {id:action.id,symbolID:action.id,package:action.id,level:action.id};
    if(action.view==='flow')query.query(report,action.nodeID||action.from||action.to?'inspect':'flow',args);
    else if(action.view==='review-map')query.query(report,'assess',{...action,symbolID:action.id});
    else if(action.view==='signals')query.query(report,'assess',{package:action.package,findingID:action.id});
    else if(action.view==='hygiene')query.query(report,'evidence',action.id?{checkID:action.id}:{});
    else if(action.view==='results') {
      if(!repository||!repository.modules?.some(m=>m.directory===report.root))throw Error('Repository run is unavailable for this snapshot');
      query.query(report,'results',action.id?{stepID:action.id}:{},repository);
    }
    else if(action.view==='dataflow')query.query(report,'dataflow',{symbolID:action.id,valueID:action.valueID,direction:action.direction,depth:action.depth});
    else if(['symbol','logical','control','calls'].includes(action.view))query.query(report,'evidence',{symbolID:action.id,
      functionView:action.view==='symbol'?undefined:action.view});
    else if(action.view==='architecture')query.query(report,'architecture',{level:action.id,componentID:action.componentID,operationID:action.operationID,group:action.group});
    return action;
  }
  const api={validate,navigationRequested};
  if(common)module.exports=api;else root.GoReviewChat=api;
})(typeof globalThis==='undefined'?this:globalThis);
