const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');

async function comparison(before,after,options={}) {
  const html=fs.readFileSync(path.join(__dirname,'index.html'),'utf8');
  const dom=new JSDOM(html,{runScripts:'outside-only',url:'http://127.0.0.1:8090'});
  const window=dom.window;
  const sides={before,after};
  window.fetch=async(url,request)=>{
    const args=request?.body?JSON.parse(request.body):{};
    if(args.operation==='source') {
      const text=sides[args.side];
      if(text===null)return {ok:false,statusText:'Not Found',json:async()=>({error:`file is absent on ${args.side}`})};
      if(options.failure===args.side)return {ok:false,statusText:'Error',json:async()=>({error:'retained content unavailable'})};
      const chars=Array.from(text),offset=args.textOffset||0,end=Math.min(chars.length,offset+(options.pageSize||10000));
      return {ok:true,json:async()=>({before:{path:before===null?'':'old.go'},after:{path:after===null?'':'new.go'},
        line:1,text:chars.slice(offset,end).join(''),textPage:{offset,total:chars.length,nextOffset:end<chars.length?end:null}})};
    }
    return {ok:true,json:async()=>url==='/api/chat/status'?{enabled:false}:url==='/api/review'?{running:false}:{}};
  };
  const ctx=dom.getInternalVMContext();
  for(const match of html.matchAll(/<script src="\/([^"]+)"><\/script>/g))
    vm.runInContext(fs.readFileSync(path.join(__dirname,match[1]),'utf8'),ctx,{filename:match[1]});
  vm.runInContext("changeState.active=true;changeState.id='comparison'",ctx);
  await vm.runInContext("changeCompare('file')",ctx);
  return dom;
}

test('comparison aligns changed lines after loading every source page and colors Go syntax',async()=>{
  const dom=await comparison('package main\nfunc f() int {\n return 1\n}\n','package main\nfunc f() int {\n return 2\n}\n',{pageSize:15});
  try {
    const reader=dom.window.document.querySelector('#change-reader');
    const rows=[...reader.querySelectorAll('.change-diff-row')];
    const changed=rows.find(row=>row.textContent.includes('return 1'));
    assert.ok(changed,'removed line is visible');
    assert.match(changed.textContent,/return 2/,'replacement must be aligned on the same row');
    assert.ok(changed.querySelector('.change-diff-removed'));
    assert.ok(changed.querySelector('.change-diff-added'));
    assert.equal(changed.querySelector('.change-diff-removed .change-diff-marker').textContent,'-');
    assert.equal(changed.querySelector('.change-diff-added .change-diff-marker').textContent,'+');
    assert.ok(reader.querySelector('.token.keyword'),'Go syntax is colored');
    assert.match(reader.textContent,/old\.go/);
    assert.match(reader.textContent,/new\.go/);
  } finally {dom.window.close();}
});

test('comparison distinguishes an absent revision from unavailable evidence',async()=>{
  const added=await comparison(null,'package main\n');
  try {
    assert.match(added.window.document.querySelector('#change-reader').textContent,/absent on before/i);
    assert.ok(added.window.document.querySelector('.change-diff-added'));
  } finally {added.window.close();}
  const missing=await comparison('package main\n','package main\n',{failure:'before'});
  try {
    const reader=missing.window.document.querySelector('#change-reader');
    assert.match(reader.textContent,/retained content unavailable/);
    assert.equal(reader.querySelectorAll('.change-diff-added,.change-diff-removed').length,0,
      'unavailable content must not be interpreted as an empty file');
  } finally {missing.window.close();}
});

test('deleted files, final newlines and untrusted source remain visible without executing markup',async()=>{
  const dom=await comparison('package main\n// <img src=x onerror=alert(1)>\nreturn 1',null);
  try {
    const reader=dom.window.document.querySelector('#change-reader');
    assert.match(reader.textContent,/absent on after/i);
    assert.match(reader.textContent,/\[no final newline\]/);
    assert.ok(reader.querySelector('.change-diff-removed'));
    assert.equal(reader.querySelectorAll('img').length,0);
    assert.match(reader.textContent,/<img src=x onerror=alert\(1\)>/);
  } finally {dom.window.close();}
});

test('large source is disclosed instead of presenting a partial diff',async()=>{
  const dom=await comparison('x'.repeat(250001),'x\n');
  try {
    const reader=dom.window.document.querySelector('#change-reader');
    assert.match(reader.textContent,/exceeds the 250,000 character diff limit/);
    assert.equal(reader.querySelectorAll('.change-diff-row').length,0);
    assert.match(reader.textContent,/Read retained source/);
    assert.match(reader.textContent,/Read targeted Git patch/);
  } finally {dom.window.close();}
});
