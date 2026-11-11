(function(root) {
  'use strict';
  const metrics=typeof module!=='undefined'&&module.exports?require('./graph-tools.js'):root.GoReviewGraph;
  function reviewRecords(report) {
    const records=[];let unmeasured=0;
    for(const symbol of report.symbols||[]) {
      if(!['function','method','closure','test'].includes(symbol.kind))continue;
      if(!Number.isFinite(symbol.cyclomatic)||symbol.cyclomatic<1){unmeasured++;continue;}
      records.push({symbol,complexity:symbol.cyclomatic,coverage:metrics.coverageMetric(symbol.location,report.coverage)});
    }
    return {records,unmeasured};
  }
  // Coordinates use uncapped raw complexity and profile-weighted coverage.
  // Coincident values share a marker, never jittered into invented measurements.
  function reviewPlot(records,filter={}) {
    const maxComplexity=Math.max(5,...records.map(r=>r.complexity));
    const query=(filter.query||'').trim().toLowerCase();
    const matches=records.filter(r=>(!filter.package||r.symbol.package===filter.package)&&
      (!query||(r.symbol.name+' '+r.symbol.qualified).toLowerCase().includes(query)));
    const groups=new Map();
    for(const record of matches) {
      const {complexity,coverage}=record,key=complexity+':'+(coverage.value===null?'unknown':coverage.value);
      if(!groups.has(key))groups.set(key,{
        key,x:100+(complexity-1)/(maxComplexity-1)*940,
        y:coverage.value===null?540:460-coverage.value*4,
        members:[]
      });
      groups.get(key).members.push(record);
    }
    return {records:matches,groups:[...groups.values()],maxComplexity};
  }
  const api={reviewRecords,reviewPlot};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewMap=api;
})(typeof globalThis==='undefined'?this:globalThis);
