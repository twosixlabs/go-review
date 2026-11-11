// Profiles source projection and hierarchy; never executes target code.
// Args: SNAPSHOT ENDPOINT_PATTERN [FOCUSED_SYMBOL_NAME].
const fs=require('node:fs');
const {endpointFlow}=require('../internal/webui/endpoint-flow-tools.js');
const {hierarchy,project}=require('../internal/webui/visual-review-tools.js');
const [file,pattern,name]=process.argv.slice(2);
if(!file||!pattern)throw Error('usage: node scripts/profile-visual-review.js SNAPSHOT ENDPOINT_PATTERN [FOCUSED_SYMBOL_NAME]');
const report=JSON.parse(fs.readFileSync(file,'utf8'));
const endpoint=report.httpEndpoints.find(e=>e.pattern===pattern);
if(!endpoint)throw Error('Endpoint not found: '+pattern);
const entry=endpointFlow(report,endpoint,{reviewActivities:true});
const symbol=name?entry.nodes.find(n=>report.symbols.find(s=>s.id===n.symbolID)?.name===name)?.symbolID:undefined;
if(name&&!symbol)throw Error('Focused operation not found: '+name);
const start=performance.now();
const raw=endpointFlow(report,endpoint,{reviewActivities:true,symbolID:symbol});
const projectedAt=performance.now(),tree=hierarchy(report,raw),hierarchyAt=performance.now(),scene=project(tree,[]);
console.log(JSON.stringify({projectionMS:projectedAt-start,hierarchyMS:hierarchyAt-projectedAt,
  groupingMS:performance.now()-hierarchyAt,rawNodes:raw.nodes.length,nodes:scene.nodes.length,
  edges:scene.edges.length,limitations:raw.limitations},null,2));
