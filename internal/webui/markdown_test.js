const test=require('node:test');
const assert=require('node:assert/strict');
const {JSDOM}=require('jsdom');
const fs=require('node:fs');
const path=require('node:path');
function browser() {
  const dom=new JSDOM('<main></main>',{runScripts:'outside-only',url:'http://127.0.0.1:8090/'});
  for(const file of ['vendor/marked.umd.js','vendor/purify.min.js','markdown.js'])
    dom.window.eval(fs.readFileSync(path.join(__dirname,file),'utf8'));
  return dom;
}
test('Markdown renders headings, tables, lists and fenced code as structured HTML',()=>{
  const dom=browser(),container=dom.window.document.querySelector('main');
  dom.window.GoReviewMarkdown.render(container,'# Builder\n\n- One\n- Two\n\n| A | B |\n|---|---|\n| x | y |\n\n```go\nfunc main() {}\n```\n\n**Strong**');
  assert.equal(container.querySelector('h1').textContent,'Builder');
  assert.equal(container.querySelectorAll('li').length,2);
  assert.equal(container.querySelector('td').textContent,'x');
  assert.equal(container.querySelector('pre code').textContent,'func main() {}\n');
  assert.equal(container.querySelector('strong').textContent,'Strong');
  dom.window.close();
});
test('untrusted Markdown cannot execute HTML, load remote assets or navigate unsafe protocols',()=>{
  const dom=browser(),container=dom.window.document.querySelector('main');
  const text=`<script>window.pwned=1</script><img src="https://invalid.example/track" onerror="window.pwned=2">
<iframe src="/api/unit-tests"></iframe><form action="/api/unit-tests"><input name="execute"></form>
<svg onload="window.pwned=3"></svg><style>body{display:none}</style>

[unsafe](javascript:alert(1)) [file](file:///etc/passwd) [relative](docs/architecture.md)
[safe](https://example.com) ![diagram](https://invalid.example/chart.svg)
<a href="data:text/html,test" onclick="window.pwned=4">data</a>`;
  dom.window.GoReviewMarkdown.render(container,text);
  assert.equal(container.querySelectorAll('script,img,iframe,form,input,svg,style').length,0);
  assert.equal(container.querySelectorAll('[onclick],[onload],[onerror],[style]').length,0);
  assert.equal(dom.window.pwned,undefined);
  const hrefs=Array.from(container.querySelectorAll('a[href]')).map(a=>a.getAttribute('href'));
  assert.deepEqual(hrefs.sort(),['docs/architecture.md','https://example.com']);
  assert.equal(container.querySelector('a[href^="https"]').getAttribute('rel'),'noopener noreferrer');
  assert.ok(container.textContent.includes('diagram'));
  dom.window.close();
});
test('relative document links resolve within the collected root with fragments preserved',()=>{
  const dom=browser(),tools=dom.window.GoReviewMarkdown;
  assert.deepEqual(JSON.parse(JSON.stringify(tools.documentLink('docs/guide.md','../README.md#overview'))),
    {path:'README.md',fragment:'overview'});
  assert.equal(tools.documentLink('README.md','../../outside.md'),null);
  assert.equal(tools.documentLink('README.md','https://example.com'),null);
  assert.equal(tools.documentLink('README.md','/api/unit-tests'),null);
  assert.deepEqual(JSON.parse(JSON.stringify(tools.documentLink('docs/guide.md','#details'))),
    {path:'docs/guide.md',fragment:'details'});
  dom.window.close();
});
