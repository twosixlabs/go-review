// Browser regression: run in a loaded Go Review page with its production assets.
// Checks Test Quality from package, diagram, and reader workspace layouts.
// CLI: node scripts/test-test-quality-layout.js LIVE_BROWSER_DATA OUTPUT_HTML [RUNNING_DATA].
// Builds an offline browser fixture from production assets and actual archived
// reader responses. It never starts a server, inference or target execution.
// testQualityHistoryLayout additionally checks the responsive history panel.
if (typeof window === 'undefined') {
  const fs = require('node:fs'), path = require('node:path');
  const [input, output, runningInput] = process.argv.slice(2);
  if (!input || !output) throw Error('LIVE_BROWSER_DATA and OUTPUT_HTML are required');
  const root = path.join(__dirname, '../internal/webui');
  const data = JSON.parse(fs.readFileSync(input, 'utf8'));
  if (!data.overview?.assessmentID || !Array.isArray(data.tests?.items))
    throw Error('Actual archived overview and test results are required');
  if (runningInput) data.running = JSON.parse(fs.readFileSync(runningInput, 'utf8'));
  const escapeScript = text => text.replace(/<\/script/gi, '<\\/script');
  const replay = `const recorded=${escapeScript(JSON.stringify(data))};
window.qualityRecordedRequests=[];
window.qualityReplayPhase='completed';
window.fetch=async function(input,options={}) {
 const url=new URL(String(input),'http://recorded.invalid');
 const args=options.body?JSON.parse(options.body):{};
 window.qualityRecordedRequests.push({path:url.pathname,args});
 let key;
 if(url.pathname==='/api/snapshot')return {ok:false,json:async()=>({error:'Archive-only replay has no current project snapshot.'})};
 else if(url.pathname==='/api/chat/status')key='chat-status';
 else if(url.pathname==='/api/test-quality/status')key='quality-status';
 else if(url.pathname==='/api/review')return {ok:true,json:async()=>({running:false,run:null})};
 else if(url.pathname==='/api/test-quality/query')key=args.operation==='events'?'events-'+(args.offset||0):args.operation==='event'?'event-'+args.offset+'-'+(args.textOffset||0):args.operation==='test'?'test-'+args.symbolID:args.operation==='source'?'source-'+args.fileID:['expectation','conditions'].includes(args.operation)?args.operation+'-'+args.expectationID:args.operation;
 if(args.operation==='events'&&!(key in recorded))return {ok:true,json:async()=>({available:false,reason:'Agent history was not included in this replay.',items:[],nextOffset:null})};
 if(!key||!(key in recorded))return {ok:false,json:async()=>({error:'Read-only recorded fixture has no endpoint: '+url.pathname+' '+(args.operation||'')})};
 const response=window.qualityReplayPhase==='running'&&recorded.running&&key in recorded.running?recorded.running[key]:recorded[key];
 return {ok:true,json:async()=>response};
};`;
  let html = fs.readFileSync(path.join(root, 'index.html'), 'utf8');
  html = html.replace(/<link rel="stylesheet" href="\/([^"]+)">/g,
    (_, file) => `<style>${fs.readFileSync(path.join(root, file), 'utf8')}</style>`);
  html = html.replace('<head>', `<head><script>${replay}</script>`);
  html = html.replace(/<script src="\/([^"]+)"><\/script>/g,
    (_, file) => `<script src="data:text/javascript;base64,${fs.readFileSync(path.join(root, file)).toString('base64')}"></script>`);
  html = html.replace('</body>', `<script src="data:text/javascript;base64,${fs.readFileSync(__filename).toString('base64')}"></script></body>`);
  fs.writeFileSync(output, html);
} else window.testQualityLayout = async function () {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const originalClasses = document.body.className;
  const layouts = [
    'visual-workspace review-inspector-open',
    'visual-workspace review-diagram',
    'visual-workspace workspace-reader review-inspector-open'
  ];
  try {
    for (const classes of layouts) {
      document.body.className = classes;
      document.getElementById('test-quality').click();
      await new Promise(resolve => requestAnimationFrame(resolve));
      assert(getComputedStyle(document.querySelector('main')).display === 'none',
        `Exploration remains visible in ${classes}`);
      const workspace = document.getElementById('test-quality-workspace');
      const start = document.getElementById('quality-start');
      assert(!workspace.hidden, `Assessment workspace is hidden in ${classes}`);
      const bounds = start.getBoundingClientRect();
      assert(bounds.height > 0 && bounds.top >= 0 && bounds.bottom <= innerHeight,
        `Start assessment is outside the viewport in ${classes}`);
      assert(workspace.scrollHeight <= workspace.clientHeight + 1,
        `Assessment workspace is compressed in ${classes}`);
      document.getElementById('quality-return').click();
      assert(workspace.hidden, `Return did not close assessment in ${classes}`);
      assert(getComputedStyle(document.querySelector('main')).display !== 'none',
        `Return did not restore exploration in ${classes}`);
    }
    return 'Test Quality replaces exploration and exposes Start in all three layouts.';
  } finally {
    document.body.className = originalClasses;
  }
};
if(typeof window!=='undefined')window.testQualityHistoryLayout=async function() {
  const assert=(condition,message)=>{if(!condition)throw Error(message);};
  await qualityOpen();
  await qualityHistoryRefresh();
  const panel=document.getElementById('quality-history'),log=document.getElementById('quality-history-log');
  const summary=document.querySelector('.quality-summary').getBoundingClientRect(),bounds=panel.getBoundingClientRect();
  const stacked=innerWidth<=900||document.body.classList.contains('chat-open');
  assert(stacked?bounds.top>=summary.bottom-1:bounds.left>=summary.right-1,'History panel overlaps the assessment summary');
  assert(bounds.width>0&&bounds.right<=document.documentElement.clientWidth,'History panel extends outside the viewport');
  assert(bounds.height<=622&&bounds.height>=360,'History panel has no bounded reading viewport');
  assert(document.documentElement.scrollWidth<=document.documentElement.clientWidth,'History layout causes horizontal page overflow');
  const messages=log.querySelectorAll('.quality-history-message');
  const responses=log.querySelectorAll('.quality-history-message:not(.quality-history-feedback)').length;
  assert(messages.length>0,'Recorded agent responses were not restored');
  assert(!document.getElementById('quality-history-status').classList.contains('error'),'History reader failed');
  assert(qualityHistoryFollowing(),'Initial history did not follow the latest response');
  assert(log.scrollHeight>log.clientHeight,'History did not get its own scroll viewport');
  const first=messages[0];
  log.scrollTop=0;log.dispatchEvent(new Event('scroll'));
  await qualityPoll();await qualityHistoryRefresh();
  assert(document.getElementById('quality-history-status').textContent.includes(responses+' responses'),'Caught-up history was lost on refresh');
  assert(log.scrollTop===0,'Polling stole the history reading position');
  assert(first===log.querySelector('.quality-history-message'),'Polling replaced retained response cards');
  assert(!document.getElementById('quality-history-latest').hidden,'Jump to latest is missing while reading older history');
  document.getElementById('quality-history-latest').click();
  assert(qualityHistoryFollowing(),'Jump to latest did not scroll the conversation');
  clearTimeout(qualityState.timer);
  return {layout:stacked?'stacked':'side-by-side',responses};
};
