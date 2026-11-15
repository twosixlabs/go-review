// Browser regression: run in a loaded Go Review page with its production assets.
// Checks Test Quality from package, diagram, and reader workspace layouts.
// CLI: node scripts/test-test-quality-layout.js LIVE_BROWSER_DATA OUTPUT_HTML.
// Builds an offline browser fixture from production assets and actual archived
// reader responses. It never starts a server, inference or target execution.
if (typeof window === 'undefined') {
  const fs = require('node:fs'), path = require('node:path');
  const [input, output] = process.argv.slice(2);
  if (!input || !output) throw Error('LIVE_BROWSER_DATA and OUTPUT_HTML are required');
  const root = path.join(__dirname, '../internal/webui');
  const data = JSON.parse(fs.readFileSync(input, 'utf8'));
  if (!data.overview?.assessmentID || !data.suite?.ratings?.length)
    throw Error('Actual archived overview and collective results are required');
  const escapeScript = text => text.replace(/<\/script/gi, '<\\/script');
  const replay = `const recorded=${escapeScript(JSON.stringify(data))};
window.qualityRecordedRequests=[];
window.fetch=async function(input,options={}) {
 const url=new URL(String(input),'http://recorded.invalid');
 const args=options.body?JSON.parse(options.body):{};
 window.qualityRecordedRequests.push({path:url.pathname,args});
 let key;
 if(url.pathname==='/api/snapshot')return {ok:false,json:async()=>({error:'Archive-only replay has no current project snapshot.'})};
 else if(url.pathname==='/api/chat/status')key='chat-status';
 else if(url.pathname==='/api/test-quality/status')key='quality-status';
 else if(url.pathname==='/api/review')return {ok:true,json:async()=>({running:false,run:null})};
 else if(url.pathname==='/api/test-quality/query')key=args.operation==='test'?'test-'+args.symbolID:args.operation==='source'?'source-'+args.fileID:['expectation','conditions'].includes(args.operation)?args.operation+'-'+args.expectationID:args.operation;
 if(!key||!(key in recorded))return {ok:false,json:async()=>({error:'Read-only recorded fixture has no endpoint: '+url.pathname+' '+(args.operation||'')})};
 return {ok:true,json:async()=>recorded[key]};
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
