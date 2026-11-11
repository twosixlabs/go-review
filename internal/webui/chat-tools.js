(function(root) {
  'use strict';
  const common=typeof module!=='undefined'&&module.exports;
  const query=common?require('./review-query-tools.js'):root.GoReviewQuery;
  function validate(report,action,snapshotID) {
    if(!snapshotID||action.snapshotID!==snapshotID)throw Error('Stale chat snapshot; start from current evidence.');
    const operation={document:'document',symbol:'evidence',package:'packages',architecture:'architecture',flow:'flow'}[action.view];
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
    return action;
  }
  const api={validate};
  if(common)module.exports=api;else root.GoReviewChat=api;
})(typeof globalThis==='undefined'?this:globalThis);
