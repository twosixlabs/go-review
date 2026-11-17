'use strict';
function qualityHistoryCurrent(state) {
  return qualityState.history===state&&qualityState.id===state.id;
}
function qualityHistoryFollowing() {
  const log=qualityEl('quality-history-log');
  return log.scrollHeight-log.scrollTop-log.clientHeight<40;
}
function qualityHistoryScroll(follow) {
  if(follow) {
    const log=qualityEl('quality-history-log');log.scrollTop=log.scrollHeight;
  }
  qualityEl('quality-history-latest').hidden=qualityHistoryFollowing();
}
function qualityHistoryFilter() {
  const agent=qualityEl('quality-history-filter').value;
  for(const entry of qualityEl('quality-history-log').querySelectorAll('[data-agent]'))
    entry.hidden=!!agent&&entry.dataset.agent!==agent;
  for(const group of qualityEl('quality-history-log').querySelectorAll('.quality-history-activity')) {
    const entries=[...group.querySelectorAll('.quality-history-entry')].filter(entry=>!entry.hidden);
    const errors=entries.filter(entry=>entry.dataset.error==='true').length;
    group.hidden=!entries.length;
    group.querySelector('summary').textContent=`Activity · ${entries.length} events${errors?' · '+errors+' errors':''}`;
  }
}
function qualityHistoryMeta(parent,row) {
  const meta=el('div',undefined,parent,'quality-history-meta');
  el('strong',row.agentID,meta);
  const time=el('time',new Date(row.time).toLocaleTimeString(),meta,'quality-history-time');
  time.dateTime=row.time;time.title=row.time;
}
async function qualityHistoryPage(state,offset,textOffset=0) {
  const page=await qualityRequest('/api/test-quality/query',{
    assessmentID:state.id,operation:'event',offset,textOffset,textLimit:6000
  },state.controller.signal);
  if(typeof page.text!=='string'||!page.textPage||
    (page.textPage.nextOffset!==null&&(!Number.isInteger(page.textPage.nextOffset)||page.textPage.nextOffset<=textOffset)))
    throw Error('Invalid retained agent event text page.');
  return page;
}
async function qualityHistoryMessage(state,row) {
  const parts=[];
  let textOffset=0;
  do {
    const page=await qualityHistoryPage(state,row.offset,textOffset);
    if(!qualityHistoryCurrent(state))return null;
    parts.push(page.text);textOffset=page.textPage.nextOffset;
  } while(textOffset!==null);
  const event=JSON.parse(parts.join(''));
  if(typeof event.message!=='string')throw Error('Retained agent response has no message.');
  return event.message;
}
function qualityHistoryActivity(state,row) {
  if(!state.group) {
    state.group=el('details',undefined,qualityEl('quality-history-log'),'quality-history-activity');
    el('summary','Activity',state.group);
  }
  const entry=el('div',undefined,state.group,'quality-history-entry');
  entry.dataset.agent=row.agentID;entry.dataset.error=String(!!row.errorPreview);
  qualityHistoryMeta(entry,row);
  const labels={start:'Started',assignment:'Assigned review',tool_call:'Calling',tool:'Returned',end:'Finished',response:'Provider response'};
  el('div',`${labels[row.kind]||row.kind}${row.tool?' · '+row.tool:''}${row.status?' · '+row.status:''}`,entry);
  if(row.errorPreview)el('p',row.errorPreview,entry);
  const body=el('div',undefined,entry);
  let textOffset=0;
  const read=button('Read retained event',async()=>{
    read.disabled=true;
    try {
      const page=await qualityHistoryPage(state,row.offset,textOffset);
      if(!qualityHistoryCurrent(state))return;
      error.textContent='';
      el('pre',page.text,body);
      textOffset=page.textPage.nextOffset;
      read.hidden=textOffset===null;read.textContent='Read more event text';
      qualityHistoryScroll(false);
    } catch(err) {
      if(qualityHistoryCurrent(state))error.textContent=err.message;
    } finally {read.disabled=false;}
  },entry);
  const error=el('p',undefined,entry);error.setAttribute('role','status');
}
async function qualityHistoryAppend(state,row) {
  if(row.kind==='message'&&row.messagePreview) {
    const text=await qualityHistoryMessage(state,row);
    if(!qualityHistoryCurrent(state)||!text.trim())return;
    const follow=qualityHistoryFollowing();
    const article=el('article',undefined,qualityEl('quality-history-log'),'quality-history-entry quality-history-message');
    article.dataset.agent=row.agentID;
    qualityHistoryMeta(article,row);
    chatMarkdown(el('div',undefined,article,'chat-text quality-history-text'),text);
    state.responses++;state.group=null;
    qualityHistoryFilter();qualityHistoryScroll(follow);
  } else if(row.kind!=='message') {
    const follow=qualityHistoryFollowing();
    qualityHistoryActivity(state,row);
    qualityHistoryFilter();qualityHistoryScroll(follow);
  }
}
// History owns its byte cursor independently of test selection and overview
// repainting. A new assessment replaces this owner before any reply can paint.
function qualityHistoryRefresh() {
  let state=qualityState.history;
  if(!state||state.id!==qualityState.id) {
    state?.controller.abort();
    state=qualityState.history={id:qualityState.id,offset:0,responses:0,group:null,controller:new AbortController()};
    qualityEl('quality-history-log').replaceChildren();
    qualityEl('quality-history-latest').hidden=true;
    qualityEl('quality-history-retry').hidden=true;
    qualityEl('quality-history-status').classList.remove('error');
    qualityEl('quality-history-status').textContent=state.id?'Loading agent history…':'Select an assessment to read its agent history.';
  }
  if(!state.id||!qualityState.active)return Promise.resolve();
  if(state.pending){state.refreshRequested=true;return state.pending;}
  state.pending=(async()=>{
    const status=qualityEl('quality-history-status'),retry=qualityEl('quality-history-retry');
    retry.hidden=true;status.classList.remove('error');
    try {
      let more;
      do {
        state.refreshRequested=false;
        const page=await qualityRequest('/api/test-quality/query',{
          assessmentID:state.id,operation:'events',offset:state.offset,limit:100
        },state.controller.signal);
        if(!qualityHistoryCurrent(state))return;
        if(page.available===false) {
          status.textContent=page.reason;
          if(state.refreshRequested&&qualityState.active)continue;
          return;
        }
        if(page.available!==true||!Array.isArray(page.items)||!Number.isInteger(page.totalBytes))
          throw Error('Invalid retained agent history page.');
        for(let i=0;i<page.items.length;i++) {
          if(!qualityState.active)return;
          await qualityHistoryAppend(state,page.items[i]);
          if(!qualityHistoryCurrent(state))return;
          state.offset=page.items[i+1]?.offset??page.nextOffset??page.totalBytes;
        }
        more=page.nextOffset!==null&&page.nextOffset!==undefined;
      } while((more||state.refreshRequested)&&qualityState.active);
      status.textContent=state.responses?`History current · ${state.responses} responses`:'No agent responses recorded yet.';
    } catch(error) {
      if(qualityHistoryCurrent(state)) {
        status.textContent='History unavailable: '+error.message;status.classList.add('error');retry.hidden=false;
      }
    } finally {state.pending=null;}
  })();
  return state.pending;
}
qualityEl('quality-history-log').onscroll=()=>qualityHistoryScroll(false);
qualityEl('quality-history-latest').onclick=()=>qualityHistoryScroll(true);
qualityEl('quality-history-filter').onchange=()=>{qualityHistoryFilter();qualityHistoryScroll(false);};
qualityEl('quality-history-retry').onclick=qualityHistoryRefresh;
